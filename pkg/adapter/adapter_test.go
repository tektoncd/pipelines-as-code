package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	bbcloudtypes "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	bbdctypes "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
	gltest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab/test"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestHandleEvent(t *testing.T) {
	t.Parallel()
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		ConfigMap: []*corev1.ConfigMap{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      info.DefaultPipelinesAscodeConfigmapName,
					Namespace: "default",
				},
				Data: map[string]string{},
			},
		},
	})
	logger, logCatcher := logger.GetLogger()

	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, "default")

	emptys := &unstructured.Unstructured{}
	emptys.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "not",
			"namespace": "console",
		},
	})
	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), emptys)
	repositories := []*v1alpha1.Repository{
		testnewrepo.NewRepo(
			testnewrepo.RepoTestcreationOpts{
				Name:             "pipelines-as-code",
				URL:              "https://nowhere.com",
				InstallNamespace: "pipelines-as-code",
			},
		),
	}

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "namespace",
				},
			},
		},
		Repositories: repositories,
		PipelineRuns: []*pipelinev1.PipelineRun{
			tektontest.MakePRStatus("namespace", "force-me", []pipelinev1.ChildStatusReference{
				tektontest.MakeChildStatusReference("first"),
				tektontest.MakeChildStatusReference("last"),
				tektontest.MakeChildStatusReference("middle"),
			}, nil),
		},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	l := listener{
		run: &params.Run{
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Log:            logger,
				Kube:           cs.Kube,
				Dynamic:        dynClient,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{
						AutoConfigureNewGitHubRepo: false,
					},
				},
				Controller: &info.ControllerInfo{
					Configmap:        info.DefaultPipelinesAscodeConfigmapName,
					Secret:           info.DefaultPipelinesAscodeSecretName,
					GlobalRepository: info.DefaultGlobalRepoName,
				},
				Kube: &info.KubeOpts{
					// TODO: we should use a global for that
					Namespace: "pipelines-as-code",
				},
			},
		},
		logger: logger,
	}
	l.run.Clients.InitClients()
	l.run.Info.InitInfo()

	// valid push event
	testEvent := github.PushEvent{Pusher: &github.CommitAuthor{Name: new("user")}}
	event, err := json.Marshal(testEvent)
	assert.NilError(t, err)

	// invalid push event which will be skipped
	skippedEvent, err := json.Marshal(github.PushEvent{})
	assert.NilError(t, err)

	tests := []struct {
		name           string
		event          []byte
		eventType      string
		eventUUID      string
		requestType    string
		statusCode     int
		wantLogSnippet string
	}{
		{
			name:        "get http call",
			requestType: "GET",
			event:       []byte("event"),
			statusCode:  200,
		},
		{
			name:        "invalid json body",
			requestType: "POST",
			event:       []byte("some random string for invalid json body"),
			statusCode:  400,
		},
		{
			name:        "invalid json body only when payload has been set",
			requestType: "POST",
			event:       []byte(""),
			statusCode:  200,
		},
		{
			name:        "valid event",
			requestType: "POST",
			eventType:   "push",
			event:       event,
			statusCode:  202,
		},
		{
			name:           "detected global repository",
			requestType:    "POST",
			eventType:      "push",
			event:          event,
			statusCode:     202,
			wantLogSnippet: "detected global repository settings named pipelines-as-code in namespace pipelines-as-code",
		},
		{
			name:        "skip event",
			requestType: "POST",
			eventType:   "push",
			event:       skippedEvent,
			statusCode:  200,
		},
		{
			name:        "git provider not detected",
			requestType: "POST",
			eventType:   "",
			event:       event,
			statusCode:  200,
		},
		{
			name:           "event time logged",
			requestType:    "POST",
			eventType:      "push",
			event:          event,
			statusCode:     202,
			eventUUID:      "1234567890",
			wantLogSnippet: "controller responded to event 1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tn := tt
			t.Parallel()

			ts := httptest.NewServer(l.handleEvent(ctx))
			defer ts.Close()

			req, err := http.NewRequestWithContext(context.Background(), tn.requestType, ts.URL, bytes.NewReader(tn.event))
			assert.NilError(t, err)
			req.Header.Set("X-Github-Event", tn.eventType)

			if tn.eventUUID != "" {
				req.Header.Set("X-GitHub-Delivery", tn.eventUUID)
			}

			resp, err := http.DefaultClient.Do(req)
			assert.NilError(t, err)
			defer resp.Body.Close()

			if tn.wantLogSnippet != "" {
				assert.Assert(t, logCatcher.FilterMessageSnippet(tn.wantLogSnippet).Len() > 0, logCatcher.All())
			}
			assert.Equal(t, resp.StatusCode, tn.statusCode)
		})
	}
}

func TestWhichProvider(t *testing.T) {
	logger, _ := logger.GetLogger()
	l := listener{
		logger: logger,
	}
	tests := []struct {
		name          string
		event         any
		header        http.Header
		wantErrString string
	}{
		{
			name: "github event",
			header: map[string][]string{
				"X-Github-Event":    {"push"},
				"X-GitHub-Delivery": {"abcd"},
			},
			event: github.PushEvent{
				Pusher: &github.CommitAuthor{Name: new("user")},
			},
		},
		{
			name: "some random event",
			header: map[string][]string{
				"foo": {"bar"},
			},
			event:         map[string]string{"foo": "bar"},
			wantErrString: "no supported Git provider has been detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jeez, err := json.Marshal(tt.event)
			if err != nil {
				assert.NilError(t, err)
			}
			req := &http.Request{
				Header: tt.header,
			}

			_, _, err = l.detectProvider(req, string(jeez))
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestDetectProviderAdditionalProviders(t *testing.T) {
	sample := gltest.TEvent{
		Username:          "foo",
		DefaultBranch:     "main",
		URL:               "https://gitlab.example.com",
		SHA:               "sha",
		SHAurl:            "https://gitlab.example.com/commit/sha",
		SHAtitle:          "commit title",
		Headbranch:        "main",
		Basebranch:        "main",
		UserID:            10,
		PathWithNameSpace: "owner/repo",
	}
	tests := []struct {
		name        string
		event       any
		eventString string
		header      http.Header
	}{
		{
			name:        "gitea push event",
			eventString: `{"pusher": {"id": 1}}`,
			header: http.Header{
				"X-Gitea-Event-Type": {"push"},
				"X-Gitea-Delivery":   {"gitea-delivery"},
			},
		},
		{
			name: "bitbucket data center push event",
			event: bbdctypes.PushRequestEvent{
				Actor: bbdctypes.UserWithLinks{ID: 111},
				Changes: []bbdctypes.PushRequestEventChange{
					{
						ToHash: "sha",
						RefID:  "refs/heads/main",
					},
				},
			},
			header: http.Header{
				"X-Event-Key":  {"repo:refs_changed"},
				"X-Request-ID": {"bbdc-request"},
			},
		},
		{
			name:        "gitlab push event",
			eventString: sample.PushEventAsJSON(true),
			header: http.Header{
				"X-Gitlab-Event":      {string(gitlab.EventTypePush)},
				"X-Gitlab-Event-UUID": {"gitlab-event"},
			},
		},
		{
			name: "bitbucket cloud push event",
			event: bbcloudtypes.PushRequestEvent{
				Push: bbcloudtypes.Push{
					Changes: []bbcloudtypes.Change{
						{
							New: bbcloudtypes.ChangeType{Name: "main"},
							Old: bbcloudtypes.ChangeType{Name: "old"},
						},
					},
				},
			},
			header: http.Header{
				"X-Event-Key":    {"repo:push"},
				"X-Request-UUID": {"bbcloud-request"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := logger.GetLogger()
			l := listener{logger: log, run: &params.Run{}}
			body := tt.eventString
			if body == "" {
				payload, err := json.Marshal(tt.event)
				assert.NilError(t, err)
				body = string(payload)
			}

			req := &http.Request{Header: tt.header}
			gotProvider, gotLogger, err := l.detectProvider(req, body)
			assert.NilError(t, err)
			assert.Assert(t, gotProvider != nil)
			assert.Assert(t, gotLogger != nil)
		})
	}
}

func TestHandleEventIncomingMissingFields(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       []byte
		statusCode int
	}{
		{
			name:       "incoming request missing required fields returns bad request",
			method:     http.MethodPost,
			path:       "/incoming",
			statusCode: http.StatusBadRequest,
		},
		{
			name:       "incoming request repo is not found",
			method:     http.MethodPost,
			path:       "/incoming?repository=not-found&branch=main&pipelinerun=plr-1&secret=secret",
			statusCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			log, _ := logger.GetLogger()
			l := listener{
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Kube:           stdata.Kube,
						Log:            log,
					},
					Info: info.Info{
						Controller: &info.ControllerInfo{GlobalRepository: info.DefaultGlobalRepoName},
						Kube:       &info.KubeOpts{Namespace: "pipelines-as-code"},
						Pac:        &info.PacOpts{},
					},
				},
				logger: log,
			}

			req := httptest.NewRequestWithContext(ctx, tt.method, "https://example.com"+tt.path, bytes.NewReader(tt.body))
			resp := httptest.NewRecorder()
			l.handleEvent(ctx)(resp, req)
			assert.Equal(t, tt.statusCode, resp.Code)
		})
	}
}

type failingResponseWriter struct {
	header http.Header
}

func (w *failingResponseWriter) Header() http.Header {
	return w.header
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

func (w *failingResponseWriter) WriteHeader(int) {}

func TestWriteResponseEncodeError(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "write error is logged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, logCatcher := logger.GetLogger()
			l := listener{logger: log}

			l.writeResponse(&failingResponseWriter{header: http.Header{}}, http.StatusAccepted, "accepted")
			assert.Assert(t, logCatcher.FilterMessageSnippet("failed to write back sink response").Len() > 0, logCatcher.All())
		})
	}
}

func TestStartGracefulShutdown(t *testing.T) {
	// pick a free port for the listener
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	assert.Assert(t, ok, "expected *net.TCPAddr")
	port := strconv.Itoa(tcpAddr.Port)
	assert.NilError(t, ln.Close())

	defer env.PatchAll(t, map[string]string{
		"SYSTEM_NAMESPACE":    "pipelinesascode",
		"PAC_CONTROLLER_PORT": port,
	})()

	ctx, _ := rtesting.SetupFakeContext(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	log, _ := logger.GetLogger()
	l := &listener{
		logger: log,
		run: &params.Run{
			Clients: clients.Clients{Kube: cs.Kube, Log: log},
			Info: info.Info{
				Pac:        &info.PacOpts{Settings: settings.Settings{}},
				Kube:       &info.KubeOpts{Namespace: "pipelinesascode"},
				Controller: &info.ControllerInfo{Configmap: "pipelines-as-code"},
			},
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Start(ctx)
	}()

	// wait until the http server is up before cancelling the context
	liveURL := "http://127.0.0.1:" + port + "/live"
	httpClient := &http.Client{Timeout: 2 * time.Second}
	up := false
	for range 50 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveURL, nil)
		assert.NilError(t, err)
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			up = resp.StatusCode == http.StatusOK
			if up {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Assert(t, up, "listener never became ready on %s", liveURL)

	cancel()

	select {
	case err := <-errCh:
		assert.NilError(t, err, "Start should return cleanly on context cancellation")
	case <-time.After(15 * time.Second):
		t.Fatal("listener did not shut down after context cancellation")
	}
}

func TestGetProviderEventIDFromHeader(t *testing.T) {
	tests := []struct {
		name   string
		header map[string][]string
		want   string
	}{
		{
			name: "github event",
			header: map[string][]string{
				"X-GitHub-Delivery": {"abcd"},
			},
			want: "abcd",
		},
		{
			name: "gitea event",
			header: map[string][]string{
				"X-Gitea-Delivery": {"abcd"},
			},
			want: "abcd",
		},
		{
			name: "gitlab event",
			header: map[string][]string{
				"X-Gitlab-Event-UUID": {"abcd"},
			},
			want: "abcd",
		},
		{
			name: "bitbucket cloud event",
			header: map[string][]string{
				"X-Request-UUID": {"abcd"},
			},
			want: "abcd",
		},
		{
			name: "bitbucket data center event",
			header: map[string][]string{
				"X-Request-Id": {"abcd"},
			},
			want: "abcd",
		},
		{
			name:   "unknown git provider event",
			header: map[string][]string{},
			want:   "00000000-0000-0000-0000-000000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			for key, values := range tt.header {
				for _, value := range values {
					header.Set(key, value)
				}
			}
			got := getProviderEventIDFromHeader(header)
			assert.Equal(t, got, tt.want)
		})
	}
}
