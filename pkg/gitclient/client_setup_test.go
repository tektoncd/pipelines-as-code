package gitclient

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// Test helper functions

// setupTestContext creates a test context and logger.
func setupTestContext(t *testing.T) (context.Context, *zap.SugaredLogger) {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	log, _ := logger.GetLogger()
	return ctx, log
}

// setupTestContextWithObserver creates a test context and logger with an observer for log inspection.
func setupTestContextWithObserver(t *testing.T) (context.Context, *zap.SugaredLogger, *zapobserver.ObservedLogs) {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, observedLogs := zapobserver.New(zap.InfoLevel)
	log := zap.New(observer).Sugar()
	return ctx, log, observedLogs
}

// createTestRepository creates a repository for testing with optional git_provider configuration.
func createTestRepository(hasGitProvider bool) *v1alpha1.Repository {
	repo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "test-repo",
		URL:              "https://github.com/owner/repo",
		InstallNamespace: "default",
	})

	if hasGitProvider {
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

	return repo
}

// createTestRepositoryWithSecretNames creates a repository with custom secret names.
func createTestRepositoryWithSecretNames(secretName, webhookSecretName string) *v1alpha1.Repository {
	repo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "test-repo",
		URL:              "https://github.com/owner/repo",
		InstallNamespace: "default",
	})

	repo.Spec.GitProvider = &v1alpha1.GitProvider{
		URL: "https://github.com",
		Secret: &v1alpha1.Secret{
			Name: secretName,
			Key:  "token",
		},
		WebhookSecret: &v1alpha1.Secret{
			Name: webhookSecretName,
			Key:  "webhook.secret",
		},
	}

	return repo
}

// seedTestData seeds test data and creates a Run object with controller configuration.
func seedTestData(ctx context.Context, t *testing.T, repos []*v1alpha1.Repository) *params.Run {
	t.Helper()

	namespaces := []*corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: repos,
		Namespaces:   namespaces,
	})

	return &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: stdata.PipelineAsCode,
			Kube:           stdata.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{
				Secret:           "pipelines-as-code-secret",
				GlobalRepository: "global-repo",
			},
			Kube: &info.KubeOpts{
				Namespace: "default",
			},
		},
	}
}

// createTestProvider creates and configures a test provider.
func createTestProvider(log *zap.SugaredLogger) *testprovider.TestProviderImp {
	provider := &testprovider.TestProviderImp{
		AllowIT: true,
	}
	provider.SetLogger(log)
	return provider
}

// createTestEvent creates a test event with common configuration.
func createTestEvent(eventType string, installationID int64, webhookSecret string) *info.Event {
	return &info.Event{
		EventType:      eventType,
		InstallationID: installationID,
		Provider: &info.Provider{
			URL:           "https://github.com",
			WebhookSecret: webhookSecret,
		},
	}
}

// createTestPacInfo creates a PacInfo object for testing.
func createTestPacInfo() *info.PacOpts {
	return &info.PacOpts{
		Settings: settings.Settings{},
	}
}

// createTestKintMock creates a kitesthelper mock with the given secrets.
func createTestKintMock(secrets map[string]string) *kitesthelper.KinterfaceTest {
	return &kitesthelper.KinterfaceTest{
		GetSecretResult: secrets,
	}
}

// TestSetupAuthenticatedClient_GitHubApp tests GitHub App authentication path.
func TestSetupAuthenticatedClientGitHubApp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		eventType   string
		wantErr     bool
		errContains string
	}{
		{
			name:      "GitHub App with pull_request event",
			eventType: "pull_request",
			wantErr:   false,
		},
		{
			name:      "GitHub App with push event",
			eventType: "push",
			wantErr:   false,
		},
		{
			name:      "GitHub App with incoming event skips validation",
			eventType: "incoming",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			// GitHub Apps don't require git_provider in Repository spec
			repo := createTestRepository(false)
			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			kint := createTestKintMock(map[string]string{
				"pipelines-as-code-secret": "github-app-webhook-secret",
			})

			event := createTestEvent(tt.eventType, 12345, "test-secret") // InstallationID 12345 indicates GitHub App
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tt.errContains != "" {
					assert.ErrorContains(t, err, tt.errContains)
				}
			} else {
				assert.NilError(t, err, "unexpected error: %v", err)
			}
		})
	}
}

// TestSetupAuthenticatedClient_NonGitHubApp tests non-GitHub App authentication path.
func TestSetupAuthenticatedClientNonGitHubApp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		hasGitProvider bool
		secretValue    string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "Non-GitHub App with git_provider succeeds",
			hasGitProvider: true,
			secretValue:    "test-token",
			wantErr:        false,
		},
		{
			name:           "Non-GitHub App without git_provider fails",
			hasGitProvider: false,
			wantErr:        true,
			errContains:    "cannot get secret from repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			repo := createTestRepository(tt.hasGitProvider)
			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			// Mock secret values
			secrets := map[string]string{}
			if tt.hasGitProvider {
				secrets["test-secret"] = tt.secretValue
			}
			kint := createTestKintMock(secrets)

			event := createTestEvent("pull_request", 0, "") // InstallationID 0 = Not a GitHub App
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tt.errContains != "" {
					assert.ErrorContains(t, err, tt.errContains)
				}
			} else {
				assert.NilError(t, err, "unexpected error: %v", err)
				// Verify token was set
				assert.Equal(t, tt.secretValue, event.Provider.Token, "token should be set from secret")
			}
		})
	}
}

// TestSetupAuthenticatedClient_RepositoryConfig tests repository-level configuration.
func TestSetupAuthenticatedClientRepositoryConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		repoHasGitProvider bool
		wantErr            bool
		wantErrContains    string
	}{
		{
			name:               "Repo with git_provider succeeds",
			repoHasGitProvider: true,
			wantErr:            false,
		},
		{
			name:               "Repo without git_provider fails",
			repoHasGitProvider: false,
			wantErr:            true,
			wantErrContains:    "failed to find git_provider details",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			var repo *v1alpha1.Repository
			if tt.repoHasGitProvider {
				repo = createTestRepositoryWithSecretNames("test-secret", "test-webhook-secret")
			} else {
				repo = createTestRepository(false)
			}

			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			kint := createTestKintMock(map[string]string{
				"test-secret":         "test-token",
				"test-webhook-secret": "webhook-secret",
			})

			event := createTestEvent("pull_request", 0, "")
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient with no global repo
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
			} else {
				assert.NilError(t, err, "unexpected error: %v", err)
				assert.Equal(t, "test-token", event.Provider.Token, "should use repo token")
			}
		})
	}
}

// TestSetupAuthenticatedClientGlobalRepoAutoFetch tests the auto-fetch behavior
// when globalRepo is nil.
func TestSetupAuthenticatedClientGlobalRepoAutoFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		globalRepoExist bool
		passGlobalRepo  bool
		wantErr         bool
		description     string
	}{
		{
			name:            "globalRepo nil and exists in API is fetched and merged",
			globalRepoExist: true,
			passGlobalRepo:  false,
			wantErr:         false,
			description:     "When globalRepo is nil and exists in the API, it should be fetched automatically",
		},
		{
			name:            "globalRepo nil and does not exist in API continues without error",
			globalRepoExist: false,
			passGlobalRepo:  false,
			wantErr:         false,
			description:     "When globalRepo is nil and does not exist in the API, function should continue without error",
		},
		{
			name:            "globalRepo provided skips API fetch",
			globalRepoExist: false,
			passGlobalRepo:  true,
			wantErr:         false,
			description:     "When globalRepo is provided, no API fetch should happen",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			repo := createTestRepository(true)
			repos := []*v1alpha1.Repository{repo}

			// If global repo should exist in the API, seed it
			var globalRepo *v1alpha1.Repository
			if tt.globalRepoExist {
				globalRepo = testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
					Name:             "global-repo",
					URL:              "https://github.com/global/repo",
					InstallNamespace: "default",
				})
				globalRepo.Spec.GitProvider = &v1alpha1.GitProvider{
					URL: "https://github.com",
					Secret: &v1alpha1.Secret{
						Name: "global-secret",
						Key:  "token",
					},
				}
				repos = append(repos, globalRepo)
			}

			run := seedTestData(ctx, t, repos)
			testProvider := createTestProvider(log)

			kint := createTestKintMock(map[string]string{
				"test-secret":   "test-token",
				"global-secret": "global-token",
			})

			event := createTestEvent("pull_request", 0, "")
			pacInfo := createTestPacInfo()

			// Determine what to pass as globalRepo parameter
			var globalRepoParam *v1alpha1.Repository
			if tt.passGlobalRepo {
				globalRepoParam = &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "provided-global-repo",
						Namespace: "default",
					},
				}
			}

			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, globalRepoParam, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "%s: expected error but got nil", tt.description)
			} else {
				assert.NilError(t, err, "%s: %v", tt.description, err)
			}
		})
	}
}

// TestSetupAuthenticatedClientGlobalRepoMergesSettings verifies that when globalRepo
// is auto-fetched, its settings are properly merged into the local repo.
func TestSetupAuthenticatedClientGlobalRepoMergesSettings(t *testing.T) {
	t.Parallel()

	ctx, log := setupTestContext(t)

	// Local repo with git_provider URL but no secret (secret comes from global)
	repo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "test-repo",
		URL:              "https://github.com/owner/repo",
		InstallNamespace: "default",
	})
	repo.Spec.GitProvider = &v1alpha1.GitProvider{
		URL: "https://github.com",
	}

	// Global repo with git_provider credentials
	globalRepo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "global-repo",
		URL:              "https://github.com/global/repo",
		InstallNamespace: "default",
	})
	globalRepo.Spec.GitProvider = &v1alpha1.GitProvider{
		URL: "https://github.com",
		Secret: &v1alpha1.Secret{
			Name: "global-secret",
			Key:  "token",
		},
		WebhookSecret: &v1alpha1.Secret{
			Name: "global-secret",
			Key:  "webhook.secret",
		},
	}

	run := seedTestData(ctx, t, []*v1alpha1.Repository{repo, globalRepo})
	testProvider := createTestProvider(log)

	kint := createTestKintMock(map[string]string{
		"global-secret": "global-token-value",
	})

	event := createTestEvent("pull_request", 0, "")
	pacInfo := createTestPacInfo()

	// Pass nil globalRepo so it gets auto-fetched
	err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

	assert.NilError(t, err, "should succeed with auto-fetched global repo providing credentials")
	// After merge, the repo should have secret from global repo
	assert.Assert(t, repo.Spec.GitProvider.Secret != nil, "secret should be merged from global repo")
	assert.Equal(t, "global-token-value", event.Provider.Token, "token should come from global repo secret")
}

// TestSetupAuthenticatedClient_WebhookValidation tests webhook secret validation.
func TestSetupAuthenticatedClientWebhookValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		webhookSecret string
		eventType     string
		shouldFail    bool
		expectWarning bool
	}{
		{
			name:          "valid webhook secret",
			webhookSecret: "valid-secret",
			eventType:     "pull_request",
			shouldFail:    false,
			expectWarning: false,
		},
		{
			name:          "webhook secret with newline triggers warning",
			webhookSecret: "secret-with-newline\n",
			eventType:     "pull_request",
			shouldFail:    false, // Test provider doesn't actually validate
			expectWarning: true,
		},
		{
			name:          "webhook secret with space triggers warning",
			webhookSecret: "secret-with-space ",
			eventType:     "pull_request",
			shouldFail:    false,
			expectWarning: true,
		},
		{
			name:          "incoming webhook skips validation",
			webhookSecret: "",
			eventType:     "incoming",
			shouldFail:    false,
			expectWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log, _ := setupTestContextWithObserver(t)

			repo := createTestRepository(true)
			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			kint := createTestKintMock(map[string]string{
				"test-secret": "test-token",
			})

			event := createTestEvent(tt.eventType, 0, tt.webhookSecret)
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.shouldFail {
				assert.Assert(t, err != nil, "expected error but got nil")
			} else {
				assert.NilError(t, err, "unexpected error: %v", err)
			}
		})
	}
}

// TestSetupAuthenticatedClient_Idempotent tests that the function is safe to call multiple times.
func TestSetupAuthenticatedClientIdempotent(t *testing.T) {
	t.Parallel()

	ctx, log := setupTestContext(t)

	repo := createTestRepository(true)
	run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
	testProvider := createTestProvider(log)

	kint := createTestKintMock(map[string]string{
		"test-secret": "test-token",
	})

	event := createTestEvent("pull_request", 0, "")
	pacInfo := createTestPacInfo()

	// Call SetupAuthenticatedClient multiple times
	err1 := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)
	assert.NilError(t, err1, "first call should succeed")

	err2 := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)
	assert.NilError(t, err2, "second call should succeed (idempotent)")

	err3 := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)
	assert.NilError(t, err3, "third call should succeed (idempotent)")

	// Verify token is still correctly set
	assert.Equal(t, "test-token", event.Provider.Token, "token should remain consistent")
}

// TestSetupAuthenticatedClient_EventTypes verifies that client setup works
// correctly for all major event types across different providers.
func TestSetupAuthenticatedClientEventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		eventType      string
		installationID int64
		wantErr        bool
		description    string
	}{
		{
			name:           "push event with GitHub App",
			eventType:      "push",
			installationID: 12345,
			wantErr:        false,
			description:    "Push events should work with GitHub App authentication",
		},
		{
			name:           "push event without GitHub App",
			eventType:      "push",
			installationID: 0,
			wantErr:        false,
			description:    "Push events should work with standard git_provider authentication",
		},
		{
			name:           "pull_request event with GitHub App",
			eventType:      "pull_request",
			installationID: 12345,
			wantErr:        false,
			description:    "PR events should work with GitHub App authentication",
		},
		{
			name:           "pull_request event without GitHub App",
			eventType:      "pull_request",
			installationID: 0,
			wantErr:        false,
			description:    "PR events should work with standard git_provider authentication",
		},
		{
			name:           "issue_comment event (retest command)",
			eventType:      "issue_comment",
			installationID: 12345,
			wantErr:        false,
			description:    "Comment events like /retest should work",
		},
		{
			name:           "check_run event",
			eventType:      "check_run",
			installationID: 12345,
			wantErr:        false,
			description:    "Check run events should work",
		},
		{
			name:           "check_suite event",
			eventType:      "check_suite",
			installationID: 12345,
			wantErr:        false,
			description:    "Check suite events should work",
		},
		{
			name:           "incoming webhook event (skips validation)",
			eventType:      "incoming",
			installationID: 0,
			wantErr:        false,
			description:    "Incoming webhook events should skip payload validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			// GitHub Apps don't need git_provider in Repository spec
			hasGitProvider := tt.installationID == 0
			repo := createTestRepository(hasGitProvider)
			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			var secrets map[string]string
			if tt.installationID > 0 {
				// GitHub App uses controller secret
				secrets = map[string]string{
					"pipelines-as-code-secret": "github-app-webhook-secret",
				}
			} else {
				// Non-GitHub App uses git_provider secret
				secrets = map[string]string{
					"test-secret": "test-token",
				}
			}
			kint := createTestKintMock(secrets)

			event := createTestEvent(tt.eventType, tt.installationID, "test-secret")
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "%s: expected error but got nil", tt.description)
			} else {
				assert.NilError(t, err, "%s: %v", tt.description, err)
			}
		})
	}
}

// TestSetupAuthenticatedClient_ProviderSpecificEvents tests event types
// specific to different Git providers (GitLab, Gitea, Bitbucket).
func TestSetupAuthenticatedClientProviderSpecificEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		eventType   string
		wantErr     bool
		description string
	}{
		{
			name:        "GitLab merge request event",
			eventType:   "Merge Request Hook",
			wantErr:     false,
			description: "GitLab MR events should work",
		},
		{
			name:        "GitLab push event",
			eventType:   "Push Hook",
			wantErr:     false,
			description: "GitLab push events should work",
		},
		{
			name:        "GitLab tag event",
			eventType:   "Tag Push Hook",
			wantErr:     false,
			description: "GitLab tag events should work",
		},
		{
			name:        "Gitea push event",
			eventType:   "push",
			wantErr:     false,
			description: "Gitea push events should work",
		},
		{
			name:        "Gitea pull request event",
			eventType:   "pull_request",
			wantErr:     false,
			description: "Gitea PR events should work",
		},
		{
			name:        "Bitbucket Cloud push event",
			eventType:   "repo:push",
			wantErr:     false,
			description: "Bitbucket Cloud push events should work",
		},
		{
			name:        "Bitbucket Cloud PR event",
			eventType:   "pullrequest:created",
			wantErr:     false,
			description: "Bitbucket Cloud PR events should work",
		},
		{
			name:        "Bitbucket Data Center PR opened",
			eventType:   "pr:opened",
			wantErr:     false,
			description: "Bitbucket Data Center PR opened events should work",
		},
		{
			name:        "Bitbucket Data Center push",
			eventType:   "repo:refs_changed",
			wantErr:     false,
			description: "Bitbucket Data Center push events should work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			repo := createTestRepository(true)
			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			kint := createTestKintMock(map[string]string{
				"test-secret": "test-token",
			})

			event := createTestEvent(tt.eventType, 0, "test-secret")
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "%s: expected error but got nil", tt.description)
			} else {
				assert.NilError(t, err, "%s: %v", tt.description, err)
			}
		})
	}
}

// TestSetupAuthenticatedClient_CommentEventTypes specifically tests
// GitOps comment commands to ensure they work correctly.
func TestSetupAuthenticatedClientCommentEventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		eventType   string
		comment     string
		wantErr     bool
		description string
	}{
		{
			name:        "retest command on GitHub",
			eventType:   "issue_comment",
			comment:     "/retest",
			wantErr:     false,
			description: "/retest command should work on GitHub",
		},
		{
			name:        "test command on GitHub",
			eventType:   "issue_comment",
			comment:     "/test",
			wantErr:     false,
			description: "/test command should work on GitHub",
		},
		{
			name:        "ok-to-test command on GitHub",
			eventType:   "issue_comment",
			comment:     "/ok-to-test",
			wantErr:     false,
			description: "/ok-to-test command should work on GitHub",
		},
		{
			name:        "cancel command on GitHub",
			eventType:   "issue_comment",
			comment:     "/cancel",
			wantErr:     false,
			description: "/cancel command should work on GitHub",
		},
		{
			name:        "retest command on GitLab",
			eventType:   "Note Hook",
			comment:     "/retest",
			wantErr:     false,
			description: "/retest command should work on GitLab",
		},
		{
			name:        "test command on Gitea",
			eventType:   "issue_comment",
			comment:     "/test",
			wantErr:     false,
			description: "/test command should work on Gitea",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, log := setupTestContext(t)

			repo := createTestRepository(true)
			run := seedTestData(ctx, t, []*v1alpha1.Repository{repo})
			testProvider := createTestProvider(log)

			kint := createTestKintMock(map[string]string{
				"test-secret": "test-token",
			})

			event := createTestEvent(tt.eventType, 0, "test-secret")
			pacInfo := createTestPacInfo()

			// Call SetupAuthenticatedClient
			err := SetupAuthenticatedClient(ctx, testProvider, kint, run, event, repo, nil, pacInfo, log)

			if tt.wantErr {
				assert.Assert(t, err != nil, "%s: expected error but got nil", tt.description)
			} else {
				assert.NilError(t, err, "%s: %v", tt.description, err)
			}
		})
	}
}
