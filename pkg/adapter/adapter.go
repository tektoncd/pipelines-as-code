package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/version"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketserver"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/eventing/pkg/adapter/v2"
	"knative.dev/pkg/logging"
)

const globalAdapterPort = "8080"

type envConfig struct {
	adapter.EnvConfig
}

func NewEnvConfig() adapter.EnvConfigAccessor {
	return &envConfig{}
}

type listener struct {
	run    *params.Run
	kint   kubeinteraction.Interface
	logger *zap.SugaredLogger
	event  *info.Event
}

type Response struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

var _ adapter.Adapter = (*listener)(nil)

func New(run *params.Run, k *kubeinteraction.Interaction) adapter.AdapterConstructor {
	return func(ctx context.Context, _ adapter.EnvConfigAccessor, _ cloudevents.Client) adapter.Adapter {
		return &listener{
			logger: logging.FromContext(ctx),
			run:    run,
			kint:   k,
		}
	}
}

func (l *listener) Start(ctx context.Context) error {
	adapterPort := globalAdapterPort
	envAdapterPort := os.Getenv("PAC_CONTROLLER_PORT")
	if envAdapterPort != "" {
		adapterPort = envAdapterPort
	}

	// Start pac config syncer
	go params.StartConfigSync(ctx, l.run)

	l.logger.Infof("Starting Pipelines as Code version: %s", strings.TrimSpace(version.Version))
	mux := http.NewServeMux()

	// for handling probes
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	mux.HandleFunc("/", l.handleEvent(ctx))

	//nolint: gosec
	srv := &http.Server{
		Addr: ":" + adapterPort,
		Handler: http.TimeoutHandler(mux,
			10*time.Second, "Listener Timeout!\n"),
	}

	enabled, tlsCertFile, tlsKeyFile := l.isTLSEnabled()
	if enabled {
		if err := srv.ListenAndServeTLS(tlsCertFile, tlsKeyFile); err != nil {
			return err
		}
	} else {
		if err := srv.ListenAndServe(); err != nil {
			return err
		}
	}
	return nil
}

func (l listener) handleEvent(ctx context.Context) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			l.writeResponse(response, http.StatusOK, "ok")
			return
		}

		// event body
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			l.logger.Errorf("failed to read body : %v", err)
			response.WriteHeader(http.StatusInternalServerError)
			return
		}

		var event map[string]interface{}
		if string(payload) != "" {
			if err := json.Unmarshal(payload, &event); err != nil {
				l.logger.Errorf("Invalid event body format format: %s", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
		}

		var gitProvider provider.Interface
		var logger *zap.SugaredLogger

		l.event = info.NewEvent()
		pacInfo := l.run.Info.GetPacOpts()

		globalRepo, err := l.run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(l.run.Info.Kube.Namespace).Get(
			ctx, l.run.Info.Controller.GlobalRepository, metav1.GetOptions{},
		)
		if err == nil && globalRepo != nil {
			l.logger.Infof("detected global repository settings named %s in namespace %s", l.run.Info.Controller.GlobalRepository, l.run.Info.Kube.Namespace)
		} else {
			globalRepo = &v1alpha1.Repository{}
		}

		detected, configuring, err := github.ConfigureRepository(ctx, l.run, request, string(payload), &pacInfo, l.logger)
		if detected {
			if configuring && err == nil {
				l.writeResponse(response, http.StatusCreated, "configured")
				return
			}
			if configuring && err != nil {
				l.logger.Errorf("repository auto-configure has failed, err: %v", err)
				l.writeResponse(response, http.StatusOK, "failed to configure")
				return
			}
			l.writeResponse(response, http.StatusOK, "skipped event")
			return
		}

		isIncoming, targettedRepo, err := l.detectIncoming(ctx, request, payload)
		if err != nil {
			l.logger.Errorf("error processing incoming webhook: %v", err)
			return
		}

		if isIncoming {
			gitProvider, logger, err = l.processIncoming(targettedRepo)
		} else {
			gitProvider, logger, err = l.detectProvider(request, string(payload))
		}

		// figure out which provider request coming from
		if err != nil || gitProvider == nil {
			l.writeResponse(response, http.StatusOK, err.Error())
			return
		}
		gitProvider.SetPacInfo(&pacInfo)

		s := sinker{
			run:        l.run,
			vcx:        gitProvider,
			kint:       l.kint,
			event:      l.event,
			logger:     logger,
			payload:    payload,
			pacInfo:    &pacInfo,
			globalRepo: globalRepo,
		}

		// clone the request to use it further
		localRequest := request.Clone(request.Context())

		go func() {
			err := s.processEvent(ctx, localRequest)
			if err != nil {
				logger.Errorf("an error occurred: %v", err)
			}
		}()

		l.writeResponse(response, http.StatusAccepted, "accepted")
	}
}

func (l listener) processRes(processEvent bool, provider provider.Interface, logger *zap.SugaredLogger, skipReason string, err error) (provider.Interface, *zap.SugaredLogger, error) {
	if processEvent {
		provider.SetLogger(logger)
		return provider, logger, nil
	}
	if err != nil {
		errStr := fmt.Sprintf("got error while processing : %v", err)
		logger.Error(errStr)
		return nil, logger, fmt.Errorf("%s", errStr)
	}

	if skipReason != "" {
		logger.Debugf("skipping non supported event: %s", skipReason)
	}
	return nil, logger, fmt.Errorf("skipping non supported event")
}

func (l listener) detectProvider(req *http.Request, reqBody string) (provider.Interface, *zap.SugaredLogger, error) {
	log := *l.logger

	// payload validation
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(reqBody), &event); err != nil {
		return nil, &log, fmt.Errorf("invalid event body format: %w", err)
	}

	gitHub := github.New()
	gitHub.Run = l.run
	isGH, processReq, logger, reason, err := gitHub.Detect(req, reqBody, &log)
	if isGH {
		return l.processRes(processReq, gitHub, logger, reason, err)
	}

	zegitea := &gitea.Provider{}
	isGitea, processReq, logger, reason, err := zegitea.Detect(req, reqBody, &log)
	if isGitea {
		return l.processRes(processReq, zegitea, logger, reason, err)
	}

	bitServer := &bitbucketserver.Provider{}
	isBitServer, processReq, logger, reason, err := bitServer.Detect(req, reqBody, &log)
	if isBitServer {
		return l.processRes(processReq, bitServer, logger, reason, err)
	}

	gitLab := &gitlab.Provider{}
	isGitlab, processReq, logger, reason, err := gitLab.Detect(req, reqBody, &log)
	if isGitlab {
		return l.processRes(processReq, gitLab, logger, reason, err)
	}

	bitCloud := &bitbucketcloud.Provider{}

	isBitCloud, processReq, logger, reason, err := bitCloud.Detect(req, reqBody, &log)
	if isBitCloud {
		return l.processRes(processReq, bitCloud, logger, reason, err)
	}

	return l.processRes(false, nil, logger, "", fmt.Errorf("no supported Git provider has been detected"))
}

func (l listener) writeResponse(response http.ResponseWriter, statusCode int, message string) {
	response.WriteHeader(statusCode)
	response.Header().Set("Content-Type", "application/json")
	body := Response{
		Status:  statusCode,
		Message: message,
	}
	if err := json.NewEncoder(response).Encode(body); err != nil {
		l.logger.Errorf("failed to write back sink response: %v", err)
	}
}
