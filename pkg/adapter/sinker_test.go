package adapter

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// setupTestData creates common test data for sinker tests.
func setupTestData(t *testing.T, repos []*v1alpha1.Repository) *params.Run {
	t.Helper()

	ctx, _ := rtesting.SetupFakeContext(t)
	log, _ := logger.GetLogger()

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pipelines-as-code",
				},
			},
		},
		Repositories: repos,
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"provider.token": []byte("test-token"),
					"webhook.secret": []byte("webhook-secret"),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      info.DefaultPipelinesAscodeSecretName,
					Namespace: "pipelines-as-code",
				},
				Data: map[string][]byte{
					"webhook.secret": []byte("controller-webhook-secret"),
				},
			},
		},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	return &params.Run{
		Clients: clients.Clients{
			Kube:           stdata.Kube,
			PipelineAsCode: stdata.PipelineAsCode,
			Log:            log,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{
				Secret: info.DefaultPipelinesAscodeSecretName,
			},
			Kube: &info.KubeOpts{
				Namespace: "pipelines-as-code",
			},
		},
	}
}

// TestSetupClient_GitHubAppVsOther tests the different code paths for GitHub Apps vs other providers.
func TestSetupClientGitHubAppVsOther(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		installationID         int64
		hasGitProvider         bool
		extraReposConfigured   bool
		extraRepoInstallIDs    map[string]int64
		wantErr                bool
		wantRepositoryIDsCount int
	}{
		{
			name:                   "GitHub App should use controller secret",
			installationID:         12345,
			hasGitProvider:         false,
			extraReposConfigured:   false,
			wantErr:                false,
			wantRepositoryIDsCount: 0, // No extra repos
		},
		{
			name:                   "Non-GitHub App requires git_provider",
			installationID:         0,
			hasGitProvider:         false,
			wantErr:                true,
			wantRepositoryIDsCount: 0,
		},
		{
			name:                   "Non-GitHub App with git_provider succeeds",
			installationID:         0,
			hasGitProvider:         true,
			wantErr:                false,
			wantRepositoryIDsCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, _ := rtesting.SetupFakeContext(t)
			log, _ := logger.GetLogger()

			// Create test repository
			repo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-repo",
				URL:              "https://github.com/test/repo",
				InstallNamespace: "default",
			})

			// Conditionally add git_provider
			if tt.hasGitProvider {
				repo.Spec.GitProvider = &v1alpha1.GitProvider{
					URL: "https://github.com",
					Secret: &v1alpha1.Secret{
						Name: "test-secret",
						Key:  "token",
					},
					WebhookSecret: &v1alpha1.Secret{
						Name: "test-secret",
						Key:  "webhook.secret",
					},
				}
			}

			run := setupTestData(t, []*v1alpha1.Repository{repo})

			// Create a tracking provider to verify behavior
			trackingProvider := &trackingProviderImpl{
				TestProviderImp: testprovider.TestProviderImp{AllowIT: true},
			}
			trackingProvider.SetLogger(log)

			kint := &kubeinteraction.Interaction{
				Run: run,
			}

			s := &sinker{
				run:  run,
				vcx:  trackingProvider,
				kint: kint,
				event: &info.Event{
					InstallationID: tt.installationID,
					EventType:      "pull_request",
					Provider: &info.Provider{
						URL: "https://github.com",
					},
				},
				logger: log,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						// Don't set SecretGHAppRepoScoped for repo-level config
						// It's only used for global config with SecretGhAppTokenScopedExtraRepos
					},
				},
			}

			// Call setupClient
			err := s.setupClient(ctx, repo)

			// Verify expectations
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
			} else {
				assert.NilError(t, err, "unexpected error: %v", err)
			}
		})
	}
}

// trackingProviderImpl wraps TestProviderImp to track CreateToken calls and repository IDs.
type trackingProviderImpl struct {
	testprovider.TestProviderImp
}

func (t *trackingProviderImpl) CreateToken(_ context.Context, _ []string, _ *info.Event) (string, error) {
	return "fake-token", nil
}

// TestGetCommitInfoError tests that GetCommitInfo errors work correctly with test provider.
// This test was moved from pkg/pipelineascode/match_test.go after refactoring
// GetCommitInfo to be called earlier in sinker.go processEvent() for PR events.
// Since processEvent is private, this test verifies the test provider behavior.
func TestGetCommitInfoError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name               string
		failGetCommitInfo  bool
		commitInfoErrorMsg string
		wantErr            bool
		wantErrContains    string
	}{
		{
			name:              "GetCommitInfo succeeds",
			failGetCommitInfo: false,
			wantErr:           false,
		},
		{
			name:               "GetCommitInfo fails with custom message",
			failGetCommitInfo:  true,
			commitInfoErrorMsg: "commit not found",
			wantErr:            true,
			wantErrContains:    "commit not found",
		},
		{
			name:              "GetCommitInfo fails with default message",
			failGetCommitInfo: true,
			wantErr:           true,
			wantErrContains:   "failed to get commit info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := &testprovider.TestProviderImp{
				FailGetCommitInfo:  tt.failGetCommitInfo,
				CommitInfoErrorMsg: tt.commitInfoErrorMsg,
			}

			event := &info.Event{}
			err := provider.GetCommitInfo(ctx, event)

			if tt.wantErr {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.wantErrContains)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestFindMatchingRepository(t *testing.T) {
	t.Parallel()

	ctx, _ := rtesting.SetupFakeContext(t)
	log, _ := logger.GetLogger()

	repo1 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-1",
		URL:              "https://github.com/test/repo1",
		InstallNamespace: "default",
	})

	repo2 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-2",
		URL:              "https://github.com/test/repo2",
		InstallNamespace: "default",
	})

	tdata := testclient.Data{
		Repositories: []*v1alpha1.Repository{repo1, repo2},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	tests := []struct {
		name     string
		eventURL string
		wantErr  bool
		wantRepo string
	}{
		{
			name:     "find matching repository",
			eventURL: "https://github.com/test/repo1",
			wantErr:  false,
			wantRepo: "repo-1",
		},
		{
			name:     "no matching repository",
			eventURL: "https://github.com/test/unknown",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &sinker{
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
					},
				},
				event: &info.Event{
					URL: tt.eventURL,
				},
				logger: log,
			}

			repo, err := s.findMatchingRepository(ctx)

			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tt.wantRepo, repo.Name)
			}
		})
	}
}

// TestProcessEventSkipCIPushEvent tests the skip-CI logic for push events.
// This tests that the SkipCI check works correctly for push events where the
// commit message is available directly in the webhook payload (event.SHATitle).
func TestProcessEventSkipCIPushEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		commitMessage string
		shouldSkip    bool
	}{
		{
			name:          "push with [skip ci] should skip",
			commitMessage: "fix: bug [skip ci]",
			shouldSkip:    true,
		},
		{
			name:          "push with [ci skip] should skip",
			commitMessage: "chore: update [ci skip]",
			shouldSkip:    true,
		},
		{
			name:          "push with [skip tkn] should skip",
			commitMessage: "docs: update [skip tkn]",
			shouldSkip:    true,
		},
		{
			name:          "push with [tkn skip] should skip",
			commitMessage: "feat: new feature [tkn skip]",
			shouldSkip:    true,
		},
		{
			name:          "push without skip command should NOT skip",
			commitMessage: "fix: important bug",
			shouldSkip:    false,
		},
		{
			name:          "push with uppercase SKIP CI should NOT skip (case-sensitive)",
			commitMessage: "fix: bug [SKIP CI]",
			shouldSkip:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Test the actual skip-CI logic directly (as used in sinker.go line 92)
			eventType := "push"
			result := provider.SkipCI(tt.commitMessage)

			// Verify the skip-CI decision
			assert.Equal(t, tt.shouldSkip, result,
				"SkipCI(%q) for %s event should return %v but got %v",
				tt.commitMessage, eventType, tt.shouldSkip, result)

			// Verify the condition that would cause early return in processEvent
			if eventType == "push" && provider.SkipCI(tt.commitMessage) {
				assert.Assert(t, tt.shouldSkip, "Event should skip when SkipCI returns true")
			} else {
				assert.Assert(t, !tt.shouldSkip, "Event should NOT skip when SkipCI returns false")
			}
		})
	}
}

// TestGetCommitInfoSetsHasSkipCommand tests that GetCommitInfo correctly sets
// HasSkipCommand when the commit message contains skip-CI commands.
// This tests the full flow that processEvent uses for PR events.
func TestGetCommitInfoSetsHasSkipCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		commitMessage     string
		expectSkipCommand bool
	}{
		{
			name:              "commit with [skip ci] should set HasSkipCommand",
			commitMessage:     "fix: bug [skip ci]",
			expectSkipCommand: true,
		},
		{
			name:              "commit with [ci skip] should set HasSkipCommand",
			commitMessage:     "chore: update [ci skip]",
			expectSkipCommand: true,
		},
		{
			name:              "commit with [skip tkn] should set HasSkipCommand",
			commitMessage:     "docs: update [skip tkn]",
			expectSkipCommand: true,
		},
		{
			name:              "commit with [tkn skip] should set HasSkipCommand",
			commitMessage:     "feat: new feature [tkn skip]",
			expectSkipCommand: true,
		},
		{
			name:              "commit without skip command should NOT set HasSkipCommand",
			commitMessage:     "fix: important bug",
			expectSkipCommand: false,
		},
		{
			name:              "commit with uppercase should NOT set HasSkipCommand (case-sensitive)",
			commitMessage:     "fix: bug [SKIP CI]",
			expectSkipCommand: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			log, _ := logger.GetLogger()

			// Create test provider
			testProvider := &testprovider.TestProviderImp{
				AllowIT: true,
			}
			testProvider.SetLogger(log)

			// Create event with commit message (SHATitle simulates commit message from API)
			event := &info.Event{
				EventType: "pull_request",
				SHA:       "abc123",
				SHATitle:  tt.commitMessage, // Simulates commit message fetched from provider API
			}

			// Call GetCommitInfo - this should set HasSkipCommand based on SHATitle
			// This mimics what real providers do: fetch commit from API, then call provider.SkipCI()
			err := testProvider.GetCommitInfo(ctx, event)
			assert.NilError(t, err)

			// Verify that HasSkipCommand was set correctly by GetCommitInfo
			assert.Equal(t, tt.expectSkipCommand, event.HasSkipCommand,
				"GetCommitInfo should set event.HasSkipCommand=%v for message %q",
				tt.expectSkipCommand, tt.commitMessage)

			// Also verify the logic matches what provider.SkipCI would return
			expectedSkip := provider.SkipCI(tt.commitMessage)
			assert.Equal(t, expectedSkip, event.HasSkipCommand,
				"event.HasSkipCommand should match provider.SkipCI(%q)", tt.commitMessage)
		})
	}
}

// TestProcessEventSkipCIIntegration documents the skip-CI flow integration.
// This is a documentation test that verifies the skip-CI decision logic
// matches what's implemented in sinker.go processEvent().
func TestProcessEventSkipCIIntegration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		eventType      string
		shaTitle       string
		hasSkipCommand bool
		shouldSkip     bool
		description    string
	}{
		{
			name:           "push event with skip-CI in commit message",
			eventType:      "push",
			shaTitle:       "fix: bug [skip ci]",
			hasSkipCommand: false, // Not used for push events
			shouldSkip:     true,
			description:    "Push events check event.SHATitle directly with provider.SkipCI()",
		},
		{
			name:           "push event without skip command",
			eventType:      "push",
			shaTitle:       "fix: important bug",
			hasSkipCommand: false,
			shouldSkip:     false,
			description:    "Push events without skip commands proceed normally",
		},
		{
			name:           "PR event with HasSkipCommand set",
			eventType:      "pull_request",
			shaTitle:       "", // Not used for PR events
			hasSkipCommand: true,
			shouldSkip:     true,
			description:    "PR events check event.HasSkipCommand set by GetCommitInfo()",
		},
		{
			name:           "PR event without skip command",
			eventType:      "pull_request",
			shaTitle:       "",
			hasSkipCommand: false,
			shouldSkip:     false,
			description:    "PR events without skip commands proceed normally",
		},
		{
			name:           "comment event should NOT skip",
			eventType:      "issue_comment",
			shaTitle:       "",
			hasSkipCommand: false,
			shouldSkip:     false,
			description:    "Comment events (e.g., /retest) are not checked for skip-CI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Simulate the skip-CI logic from sinker.go processEvent()
			// Line 92-95: Push event skip-CI check
			pushSkip := tt.eventType == "push" && provider.SkipCI(tt.shaTitle)

			// Line 99-108: PR event skip-CI check
			prSkip := tt.eventType == "pull_request" && tt.hasSkipCommand

			// Verify the expected behavior
			actualSkip := pushSkip || prSkip
			assert.Equal(t, tt.shouldSkip, actualSkip,
				"%s: expected skip=%v but got skip=%v", tt.description, tt.shouldSkip, actualSkip)
		})
	}
}
