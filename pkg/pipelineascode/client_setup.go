package pipelineascode

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// SetupAuthenticatedClient sets up the authenticated VCS client with proper token scoping.
// This is the centralized place for all client authentication and token scoping logic.
//
// This function is idempotent and safe to call multiple times.
func SetupAuthenticatedClient(
	ctx context.Context,
	vcx provider.Interface,
	kint kubeinteraction.Interface,
	run *params.Run,
	event *info.Event,
	repo *v1alpha1.Repository,
	globalRepo *v1alpha1.Repository,
	pacInfo *info.PacOpts,
	logger *zap.SugaredLogger,
) error {
	// Determine secret namespace BEFORE merging repos
	// This preserves the ability to detect when credentials come from global repo
	secretNS := repo.GetNamespace()
	if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil &&
		globalRepo != nil && globalRepo.Spec.GitProvider != nil && globalRepo.Spec.GitProvider.Secret != nil {
		secretNS = globalRepo.GetNamespace()
	}
	logger.Debugf("setupAuthenticatedClient: repo=%s/%s secret_namespace=%s", repo.GetNamespace(), repo.GetName(), secretNS)
	// merge global repo settings into local repo (after determining secret namespace)
	if globalRepo != nil {
		logger.Debugf("setupAuthenticatedClient: merging global repo settings from %s/%s", globalRepo.GetNamespace(), globalRepo.GetName())
		repo.Spec.Merge(globalRepo.Spec)
	}

	// GitHub Apps use controller secret, not Repository git_provider
	if event.InstallationID > 0 {
		logger.Debugf("setupAuthenticatedClient: github app installation id=%d, using controller webhook secret", event.InstallationID)
		event.Provider.WebhookSecret, _ = GetCurrentNSWebhookSecret(ctx, kint, run)
	} else {
		// Non-GitHub App providers use git_provider section in Repository spec
		scm := SecretFromRepository{
			K8int:       kint,
			Config:      vcx.GetConfig(),
			Event:       event,
			Repo:        repo,
			WebhookType: pacInfo.WebhookType,
			Logger:      logger,
			Namespace:   secretNS,
		}
		if err := scm.Get(ctx); err != nil {
			return fmt.Errorf("cannot get secret from repository: %w", err)
		}
		logger.Debugf("setupAuthenticatedClient: loaded git provider credentials for repo=%s/%s", repo.GetNamespace(), repo.GetName())
	}

	// Set up the authenticated client
	eventEmitter := events.NewEventEmitter(run.Clients.Kube, logger)

	// Validate payload with webhook secret (skip for incoming webhooks)
	if event.EventType != "incoming" {
		logger.Debugf("setupAuthenticatedClient: validating webhook payload for event_type=%s", event.EventType)
		if err := vcx.Validate(ctx, run, event); err != nil {
			// check that webhook secret has no /n or space into it
			if strings.ContainsAny(event.Provider.WebhookSecret, "\n ") {
				msg := `we have failed to validate the payload with the webhook secret,
it seems that we have detected a \n or a space at the end of your webhook secret, 
is that what you want? make sure you use -n when generating the secret, eg: echo -n secret|base64`
				eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositorySecretValidation", msg)
			}
			return fmt.Errorf("could not validate payload, check your webhook secret?: %w", err)
		}
	}
	// Set up the authenticated client
	clientErr := vcx.SetClient(ctx, run, event, repo, eventEmitter)
	if name := vcx.GetConfig().Name; name != "" {
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(semconv.VCSProviderNameKey.String(name))
		}
	}
	if clientErr != nil {
		return fmt.Errorf("failed to set client: %w", clientErr)
	}
	logger.Debugf("setupAuthenticatedClient: provider client initialized")

	// Handle GitHub App token scoping for both global and repo-level configuration
	if event.InstallationID > 0 {
		logger.Debugf("setupAuthenticatedClient: scoping github app token")
		token, err := github.ScopeTokenToListOfRepos(ctx, vcx, pacInfo, repo, run, event, eventEmitter, logger)
		if err != nil {
			return fmt.Errorf("failed to scope token: %w", err)
		}
		// If Global and Repo level configurations are not provided then lets not override the provider token.
		if token != "" {
			event.Provider.Token = token
		}
	}

	return nil
}
