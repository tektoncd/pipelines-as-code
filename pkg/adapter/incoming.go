package adapter

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	apincoming "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/incoming"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github/app"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	ktypes "github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	"go.uber.org/zap"
)

const (
	defaultIncomingWebhookSecretKey = "secret"
)

var errMissingFields = errors.New("missing required fields")

func errMissingSpecificFields(fields []string) error {
	return fmt.Errorf("%w: %s", errMissingFields, fields)
}

type incomingPayload struct {
	legacyMode bool // indicates the request was made using the deprecated queryparams method

	RepoName    string         `json:"repository"`
	Namespace   string         `json:"namespace,omitempty"` // Optional unless Repository name is not unique
	Branch      string         `json:"branch"`
	PipelineRun string         `json:"pipelinerun"`
	Secret      string         `json:"secret"`
	Params      map[string]any `json:"params"`
}

func (payload *incomingPayload) validate() error {
	missingFields := []string{}

	for field, value := range map[string]string{
		"repository":  payload.RepoName,
		"branch":      payload.Branch,
		"pipelinerun": payload.PipelineRun,
		"secret":      payload.Secret,
	} {
		if value == "" {
			missingFields = append(missingFields, field)
		}
	}

	if len(missingFields) > 0 {
		return errMissingSpecificFields(missingFields)
	}
	return nil
}

// parseIncomingPayload parses and validates the incoming payload.
func parseIncomingPayload(request *http.Request, payloadBody []byte) (incomingPayload, error) {
	parsedPayload := incomingPayload{
		RepoName:    request.URL.Query().Get("repository"),
		Branch:      request.URL.Query().Get("branch"),
		PipelineRun: request.URL.Query().Get("pipelinerun"),
		Secret:      request.URL.Query().Get("secret"),
		Namespace:   request.URL.Query().Get("namespace"),
		legacyMode:  true,
	}

	if parsedPayload.validate() != nil {
		if request.Method == http.MethodPost && request.Header.Get("Content-Type") == "application/json" && len(payloadBody) > 0 {
			parsedPayload = incomingPayload{legacyMode: false}
			if err := json.Unmarshal(payloadBody, &parsedPayload); err != nil {
				return parsedPayload, fmt.Errorf("invalid JSON body for incoming webhook: %w", err)
			}
		}
	}

	return parsedPayload, parsedPayload.validate()
}

func compareSecret(incomingSecret, secretValue string) bool {
	return subtle.ConstantTimeCompare([]byte(incomingSecret), []byte(secretValue)) != 0
}

func applyIncomingParams(req *http.Request, payloadBody []byte, params []string) (apincoming.Payload, error) {
	if req.Header.Get("Content-Type") != "application/json" {
		return apincoming.Payload{}, fmt.Errorf("invalid content type, only application/json is accepted when posting a body")
	}
	payload, err := apincoming.ParseIncomingPayload(payloadBody)
	if err != nil {
		return apincoming.Payload{}, fmt.Errorf("error parsing incoming payload, not the expected format?: %w", err)
	}
	for k := range payload.Params {
		if !slices.Contains(params, k) {
			return apincoming.Payload{}, fmt.Errorf("param %s is not allowed in incoming webhook CR", k)
		}
	}
	return payload, nil
}

// detectIncoming checks if the request is for an "incoming" webhook request.
// If the request is for an "incoming" webhook request the request is parsed and matched to the expected
// repository.
func (l *listener) detectIncoming(ctx context.Context, event *info.Event, req *http.Request, payloadBody []byte) (bool, *v1alpha1.Repository, error) {
	if req.URL.Path != "/incoming" {
		return false, nil, nil
	}

	l.logger.Infof("incoming request has been requested: %v", req.URL)
	payload, err := parseIncomingPayload(req, payloadBody)
	if payload.legacyMode {
		// Log this, even if the request is invalid
		l.logger.Warnf("[SECURITY] Incoming webhook used legacy URL-based secret passing. This is insecure and will be deprecated. Please use POST body instead.")
	}
	if err != nil {
		return false, nil, err
	}

	repo, err := matcher.GetRepoByName(ctx, l.run, payload.RepoName, payload.Namespace)
	if err != nil {
		if errors.Is(err, matcher.ErrRepositoryNameConflict) {
			return false, nil, fmt.Errorf("%w: %w", err, errMissingSpecificFields([]string{"namespace"}))
		}
		return false, nil, fmt.Errorf("error getting repo: %w", err)
	}
	if repo == nil {
		return false, nil, fmt.Errorf("cannot find repository %s", payload.RepoName)
	}

	if repo.Spec.Incomings == nil {
		return false, nil, fmt.Errorf("you need to have incoming webhooks rules in your repo spec, repo: %s", payload.RepoName)
	}

	hook := matcher.IncomingWebhookRule(payload.Branch, *repo.Spec.Incomings)
	if hook == nil {
		return false, nil, fmt.Errorf("branch '%s' has not matched any rules in repo incoming webhooks spec: %+v", payload.Branch, *repo.Spec.Incomings)
	}

	// log incoming request
	l.logger.Infof("incoming request targeting pipelinerun %s on branch %s for repository %s has been accepted", payload.PipelineRun, payload.Branch, payload.RepoName)

	secretOpts := ktypes.GetSecretOpt{
		Namespace: repo.Namespace,
		Name:      hook.Secret.Name,
		Key:       hook.Secret.Key,
	}

	if secretOpts.Key == "" {
		secretOpts.Key = defaultIncomingWebhookSecretKey
	}

	secretValue, err := l.kint.GetSecret(ctx, secretOpts)
	if err != nil {
		return false, nil, fmt.Errorf("error getting secret referenced in incoming-webhook: %w", err)
	}
	if secretValue == "" {
		return false, nil, fmt.Errorf("secret referenced in incoming-webhook %s is empty or key %s is not existent", hook.Secret.Name, hook.Secret.Key)
	}

	// TODO: move to somewhere common to share between gitlab and here
	if !compareSecret(payload.Secret, secretValue) {
		return false, nil, fmt.Errorf("secret passed to the webhook does not match the incoming webhook secret set on repository CR in secret %s", hook.Secret.Name)
	}

	if repo.Spec.GitProvider == nil || repo.Spec.GitProvider.Type == "" {
		gh := github.New()
		gh.Run = l.run
		ns := info.GetNS(ctx)
		ip := app.NewInstallation(req, l.run, repo, gh, ns)
		enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
		if err != nil {
			return false, nil, err
		}
		event.Provider.URL = enterpriseURL
		event.Provider.Token = token
		event.InstallationID = installationID
		// Github app is not installed for provided repository url
		if event.InstallationID == 0 {
			return false, nil, fmt.Errorf("GithubApp is not installed for the provided repository url %s ", repo.Spec.URL)
		}
	}

	// make sure accepted is json
	if string(payloadBody) != "" {
		if event.Event, err = applyIncomingParams(req, payloadBody, hook.Params); err != nil {
			return false, nil, err
		}
	}

	// TODO: more than i think about it and more i think triggertarget should be
	// eventType and vice versa, but keeping as is for now.
	event.EventType = "incoming"
	event.TriggerTarget = "push"
	event.TargetPipelineRun = payload.PipelineRun
	event.HeadBranch = payload.Branch
	event.BaseBranch = payload.Branch
	event.Request.Header = req.Header
	event.Request.Payload = payloadBody
	event.URL = repo.Spec.URL
	event.Sender = "incoming"

	return true, repo, err
}

func (l *listener) processIncoming(event *info.Event, targetRepo *v1alpha1.Repository) (provider.Interface, *zap.SugaredLogger, error) {
	// can a git ssh URL be a Repo URL? I don't think this will even ever work
	org, repo, err := formatting.GetRepoOwnerSplitted(targetRepo.Spec.URL)
	if err != nil {
		return nil, nil, err
	}
	event.Organization = org
	event.Repository = repo

	var provider provider.Interface
	if targetRepo.Spec.GitProvider == nil || targetRepo.Spec.GitProvider.Type == "" {
		provider = github.New()
	} else {
		switch targetRepo.Spec.GitProvider.Type {
		case "github":
			provider = github.New()
		case "gitlab":
			provider = &gitlab.Provider{}
		case "gitea", "forgejo":
			provider = &gitea.Provider{}
		case "bitbucket-cloud":
			provider = &bitbucketcloud.Provider{}
		case "bitbucket-datacenter":
			provider = &bitbucketdatacenter.Provider{}
		default:
			return l.processRes(false, nil, l.logger.With("namespace", targetRepo.Namespace), "", fmt.Errorf("no supported Git provider has been detected"))
		}
	}

	return l.processRes(true, provider, l.logger.With("provider", "incoming", "namespace", targetRepo.Namespace), "", nil)
}
