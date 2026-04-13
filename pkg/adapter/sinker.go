package adapter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/gitclient"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type sinker struct {
	run        *params.Run
	vcx        provider.Interface
	kint       kubeinteraction.Interface
	event      *info.Event
	logger     *zap.SugaredLogger
	payload    []byte
	pacInfo    *info.PacOpts
	globalRepo *v1alpha1.Repository
}

func (s *sinker) processEventPayload(ctx context.Context, request *http.Request) error {
	var err error
	s.event, err = s.vcx.ParsePayload(ctx, s.run, request, string(s.payload))
	if err != nil {
		s.logger.Errorf("failed to parse event: %v", err)
		return err
	}
	// If ParsePayload returned nil event (intentional skip), exit early
	if s.event == nil {
		return nil
	}

	// Enhanced structured logging with source repository context for operators
	logFields := []interface{}{
		"event-sha", s.event.SHA,
		"event-type", s.event.EventType,
		"source-repo-url", s.event.URL,
	}

	// Add branch information if available
	if s.event.BaseBranch != "" {
		logFields = append(logFields, "target-branch", s.event.BaseBranch)
	}
	// For PRs, also include source branch if different
	if s.event.HeadBranch != "" && s.event.HeadBranch != s.event.BaseBranch {
		logFields = append(logFields, "source-branch", s.event.HeadBranch)
	}

	s.logger = s.logger.With(logFields...)
	s.vcx.SetLogger(s.logger)

	s.event.Request = &info.Request{
		Header:  request.Header,
		Payload: bytes.TrimSpace(s.payload),
	}
	return nil
}

func (s *sinker) processEvent(ctx context.Context, request *http.Request) error {
	if s.event.EventType == "incoming" {
		if request.Header.Get("X-GitHub-Enterprise-Host") != "" {
			s.event.Provider.URL = request.Header.Get("X-GitHub-Enterprise-Host")
			s.event.GHEURL = request.Header.Get("X-GitHub-Enterprise-Host")
		}
	} else {
		if err := s.processEventPayload(ctx, request); err != nil {
			return err
		}
		if s.event == nil {
			return nil
		}

		// For ALL events: Setup authenticated client early (including token scoping)
		// This centralizes client setup and token scoping in one place for all event types
		repo, err := s.findMatchingRepository(ctx)
		if err != nil {
			// Continue with normal flow - repository matching will be handled in matchRepoPR
			s.logger.Debugf("Could not find matching repository: %v", err)
		} else {
			// We found the repository, now setup client with token scoping
			// If setup fails here, it's a configuration error and we should fail fast
			if err := s.setupClient(ctx, repo); err != nil {
				return fmt.Errorf("client setup failed: %w", err)
			}
			s.logger.Debugf("Client setup completed for event type: %s", s.event.EventType)
		}

		// For PUSH events: commit message is already in event.SHATitle from the webhook payload
		// We can check immediately without any API calls or repository lookups
		if s.event.EventType == "push" && provider.SkipCI(s.event.SHATitle) {
			s.logger.Infof("CI skipped for push event: commit %s contains skip command in message", s.event.SHA)
			return s.createSkipCIStatus(ctx)
		}

		// For PULL REQUEST events: commit message needs to be fetched via API
		// Get commit info for skip-CI detection (only if we successfully set up client above)
		if (s.event.EventType == "pull_request" || s.event.EventType == "Merge Request") && repo != nil {
			// Get commit info (including commit message) via API
			if err := s.vcx.GetCommitInfo(ctx, s.event); err != nil {
				return fmt.Errorf("could not get commit info: %w", err)
			}
			// Check for skip-ci commands in pull request events
			if s.event.HasSkipCommand {
				s.logger.Infof("CI skipped for pull request event: commit %s contains skip command in message", s.event.SHA)
				return s.createSkipCIStatus(ctx)
			}
		}
	}

	// Enrich span with VCS attributes — for incoming events these are
	// pre-populated; for webhook events ParsePayload filled them in.
	setVCSSpanAttributes(ctx, s.event)

	p := pipelineascode.NewPacs(s.event, s.vcx, s.run, s.pacInfo, s.kint, s.logger, s.globalRepo)
	return p.Run(ctx)
}

// findMatchingRepository finds the Repository CR that matches the event.
// This is a lightweight lookup to get credentials for early skip-ci checks.
// Uses the canonical matcher implementation to avoid code duplication.
func (s *sinker) findMatchingRepository(ctx context.Context) (*v1alpha1.Repository, error) {
	// Use canonical matcher to find repository (empty string searches all namespaces)
	repo, err := matcher.MatchEventURLRepo(ctx, s.run, s.event, "")
	if err != nil {
		return nil, fmt.Errorf("failed to match repository: %w", err)
	}
	if repo == nil {
		return nil, fmt.Errorf("no repository found matching URL: %s", s.event.URL)
	}

	return repo, nil
}

// setupClient sets up the authenticated client with token scoping for ALL event types.
// This is the primary location where client setup and GitHub App token scoping happens.
// Centralizing this here ensures consistent behavior across all events and enables early
// optimizations like skip-CI detection before expensive processing.
func (s *sinker) setupClient(ctx context.Context, repo *v1alpha1.Repository) error {
	return gitclient.SetupAuthenticatedClient(
		ctx,
		s.vcx,
		s.kint,
		s.run,
		s.event,
		repo,
		s.globalRepo,
		s.pacInfo,
		s.logger,
	)
}

// createSkipCIStatus creates a neutral status check on the git provider when CI is skipped.
func (s *sinker) createSkipCIStatus(ctx context.Context) error {
	statusOpts := status.StatusOpts{
		Status:     "completed",
		Conclusion: status.ConclusionSkipped,
		Title:      "CI Skipped",
		Summary:    fmt.Sprintf("%s - CI has been skipped", s.pacInfo.ApplicationName),
		Text:       "Commit contains a skip CI command. Use /test or /retest to manually trigger CI if needed.",
		DetailsURL: s.run.Clients.ConsoleUI().URL(),
	}

	if err := s.vcx.CreateStatus(ctx, s.event, statusOpts); err != nil {
		s.logger.Warnf("Failed to create skip-CI status: %v", err)
		// Don't return error - skip-CI should succeed even if status creation fails
		return nil
	}

	return nil
}

func setVCSSpanAttributes(ctx context.Context, event *info.Event) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(tracing.PACEventTypeKey.String(event.EventType))
	if event.URL != "" {
		span.SetAttributes(semconv.VCSRepositoryURLFullKey.String(event.URL))
	}
	if event.SHA != "" {
		span.SetAttributes(semconv.VCSRefHeadRevisionKey.String(event.SHA))
	}
}
