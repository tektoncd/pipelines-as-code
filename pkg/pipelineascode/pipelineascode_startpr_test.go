package pipelineascode

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// KinterfaceTestWithSecretTracking tracks secret creation attempts and simulates AlreadyExists for concurrent access.
type KinterfaceTestWithSecretTracking struct {
	kitesthelper.KinterfaceTest
	// secretCreationCount uses atomic.Int32 to ensure thread-safe counting across concurrent goroutines
	// without requiring explicit locks. This is critical for accurately simulating race conditions in tests
	// where multiple goroutines may attempt to create secrets simultaneously.
	secretCreationCount atomic.Int32
}

func (k *KinterfaceTestWithSecretTracking) CreateSecret(ctx context.Context, ns string, secret *corev1.Secret) error {
	// Atomically increment and get the new count
	count := k.secretCreationCount.Add(1)

	// First creation succeeds, subsequent ones return AlreadyExists to simulate race condition
	if count > 1 {
		return errors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "secrets"}, secret.GetName())
	}

	return k.KinterfaceTest.CreateSecret(ctx, ns, secret)
}

func intPtr(i int) *int {
	return &i
}

// startPRTestConfig holds optional configuration for startPR test setup.
type startPRTestConfig struct {
	// KinterfaceTest fields
	consoleURL        string
	createSecretError error
	updateSecretError error
	deleteSecretError error

	// PacOpts fields
	secretAutoCreation bool

	// Provider fields
	createStatusErorring bool
}

// defaultStartPRTestConfig returns defaults for most tests.
func defaultStartPRTestConfig() startPRTestConfig {
	return startPRTestConfig{
		consoleURL:         "https://console.test",
		secretAutoCreation: true,
	}
}

// startPRTestFixture holds all the components needed for a startPR test.
type startPRTestFixture struct {
	cs         *params.Run
	event      *info.Event
	logger     *zap.SugaredLogger
	ctx        context.Context
	fakeclient *github.Client
	teardown   func()
	kint       *kitesthelper.KinterfaceTest
	pacInfo    *info.PacOpts
	vcx        provider.Interface
	pac        PacRun
}

func setupStartPRTestWithConfig(t *testing.T, config startPRTestConfig) *startPRTestFixture {
	t.Helper()
	cs, event, logger, ctx, fakeclient, teardown := setupStartPRTest(t)

	kint := &kitesthelper.KinterfaceTest{
		ConsoleURL:        config.consoleURL,
		CreateSecretError: config.createSecretError,
		UpdateSecretError: config.updateSecretError,
		DeleteSecretError: config.deleteSecretError,
	}

	pacInfo := &info.PacOpts{
		Settings: settings.Settings{
			SecretAutoCreation: config.secretAutoCreation,
		},
	}

	var vcx provider.Interface
	if config.createStatusErorring {
		vcx = &testprovider.TestProviderImp{
			CreateStatusErorring: true,
		}
		vcx.SetPacInfo(pacInfo)
	} else {
		vcx = setupProviderForTest(cs, logger, fakeclient, pacInfo)
	}

	return &startPRTestFixture{
		cs:         cs,
		event:      event,
		logger:     logger,
		ctx:        ctx,
		fakeclient: fakeclient,
		teardown:   teardown,
		kint:       kint,
		pacInfo:    pacInfo,
		vcx:        vcx,
		pac:        NewPacs(event, vcx, cs, pacInfo, kint, logger, nil),
	}
}

// setupStartPRTestDefault creates a test fixture with default configuration.
// Use this for most tests - it provides a complete, ready-to-use test environment.
func setupStartPRTestDefault(t *testing.T) *startPRTestFixture {
	t.Helper()
	return setupStartPRTestWithConfig(t, defaultStartPRTestConfig())
}

// setupStartPRTest creates common test fixtures for startPR tests.
func setupStartPRTest(t *testing.T) (*params.Run, *info.Event, *zap.SugaredLogger, context.Context, *github.Client, func()) {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-namespace",
				},
			},
		},
	})

	cs := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: stdata.PipelineAsCode,
			Log:            logger,
			Kube:           stdata.Kube,
			Tekton:         stdata.Pipeline,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{
				Name:      "default",
				Configmap: "pipelines-as-code",
				Secret:    "pipelines-as-code-secret",
			},
		},
	}
	cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

	event := &info.Event{
		SHA:               "test-sha",
		Organization:      "test-org",
		Repository:        "test-repo",
		URL:               "https://test.com/repo",
		HeadBranch:        "test-branch",
		BaseBranch:        "main",
		Sender:            "test-user",
		EventType:         "pull_request",
		TriggerTarget:     "pull_request",
		PullRequestNumber: 123,
		Provider: &info.Provider{
			Token: "test-token",
			User:  "git",
			URL:   ghTestServerURL,
		},
	}

	// Setup common GitHub API mock responses
	replyString(mux, fmt.Sprintf("/repos/%s/%s/statuses/%s", event.Organization, event.Repository, event.SHA), "{}")
	replyString(mux, fmt.Sprintf("/repos/%s/%s/check-runs", event.Organization, event.Repository), `{"id": 123}`)

	return cs, event, logger, ctx, fakeclient, teardown
}

// setupProviderForTest creates and configures a GitHub provider for testing.
func setupProviderForTest(cs *params.Run, logger *zap.SugaredLogger, fakeclient *github.Client, pacInfo *info.PacOpts) *ghprovider.Provider {
	vcx := &ghprovider.Provider{
		Run:    cs,
		Token:  github.Ptr("test-token"),
		Logger: logger,
	}
	vcx.SetGithubClient(fakeclient)
	vcx.SetPacInfo(pacInfo)
	return vcx
}

// createTestMatch creates a Match object for testing startPR.
func createTestMatch(concurrencyLimit *int) matcher.Match {
	namespace := "test-namespace"
	prName := "test-pr-"
	annotations := make(map[string]string)
	labels := make(map[string]string)

	annotations[keys.GitAuthSecret] = "test-git-secret"

	pr := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prName,
			Namespace:    namespace,
			Annotations:  annotations,
			Labels:       labels,
		},
		Spec: pipelinev1.PipelineRunSpec{
			PipelineRef: &pipelinev1.PipelineRef{
				Name: "test-pipeline",
			},
		},
	}

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: namespace,
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: concurrencyLimit,
		},
	}

	return matcher.Match{
		PipelineRun: pr,
		Repo:        repo,
		Config:      map[string]string{},
	}
}

func TestStartPR(t *testing.T) {
	fixture := setupStartPRTestDefault(t)
	defer fixture.teardown()

	match := createTestMatch(nil)

	pr, err := fixture.pac.startPR(fixture.ctx, match)

	assert.NilError(t, err)
	assert.Assert(t, pr != nil)
	assert.Equal(t, pr.GetNamespace(), "test-namespace")
	// GenerateName is used before K8s generates the actual name
	assert.Assert(t, pr.GetGenerateName() != "" || pr.GetName() != "", "PipelineRun should have a GenerateName or Name")

	_, hasState := pr.GetAnnotations()[keys.State]
	assert.Assert(t, hasState, "State annotation should be set")

	_, hasLogURL := pr.GetAnnotations()[keys.LogURL]
	assert.Assert(t, hasLogURL, "LogURL annotation should be set")
}

func TestStartPR_AnnotationAndLabelPropagation(t *testing.T) {
	tests := []struct {
		name                   string
		setupMatch             func(*matcher.Match)
		setupEvent             func(*info.Event)
		verifyAnnotations      map[string]string // Expected annotations
		verifyLabels           map[string]string // Expected labels
		verifyAnnotationsExist []string          // Annotations that should exist (with any value)
	}{
		{
			name: "CancelInProgress annotation propagated to label",
			setupMatch: func(m *matcher.Match) {
				m.PipelineRun.Annotations[keys.CancelInProgress] = "true"
			},
			setupEvent: nil,
			verifyLabels: map[string]string{
				keys.CancelInProgress: "true",
			},
		},
		{
			name:       "GitLab project IDs set as annotations",
			setupMatch: nil,
			setupEvent: func(e *info.Event) {
				e.SourceProjectID = 123
				e.TargetProjectID = 456
			},
			verifyAnnotations: map[string]string{
				keys.SourceProjectID: "123",
				keys.TargetProjectID: "456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupStartPRTestDefault(t)
			defer fixture.teardown()

			match := createTestMatch(nil)

			if tt.setupMatch != nil {
				tt.setupMatch(&match)
			}
			if tt.setupEvent != nil {
				tt.setupEvent(fixture.event)
				// Recreate the pac instance with updated event
				fixture.pac = NewPacs(fixture.event, fixture.vcx, fixture.cs, fixture.pacInfo, fixture.kint, fixture.logger, nil)
			}

			pr, err := fixture.pac.startPR(fixture.ctx, match)

			assert.NilError(t, err)
			assert.Assert(t, pr != nil)

			for key, expectedValue := range tt.verifyAnnotations {
				value, ok := pr.GetAnnotations()[key]
				assert.Assert(t, ok, "%s annotation should be set", key)
				assert.Equal(t, value, expectedValue)
			}

			for key, expectedValue := range tt.verifyLabels {
				value, ok := pr.GetLabels()[key]
				assert.Assert(t, ok, "%s label should be set", key)
				assert.Equal(t, value, expectedValue)
			}

			for _, key := range tt.verifyAnnotationsExist {
				_, ok := pr.GetAnnotations()[key]
				assert.Assert(t, ok, "%s annotation should exist", key)
			}
		})
	}
}

func TestStartPR_ConcurrencyLimitBehavior(t *testing.T) {
	tests := []struct {
		name                    string
		concurrencyLimit        *int
		expectedState           string
		expectedStatus          pipelinev1.PipelineRunSpecStatus
		verifySCMStartedMissing bool   // true if SCMReportingPLRStarted should NOT be set
		expectedSCMStartedVal   string // expected value for SCMReportingPLRStarted when it should be set
	}{
		{
			name:                    "nil concurrency limit - starts immediately",
			concurrencyLimit:        nil,
			expectedState:           kubeinteraction.StateStarted,
			expectedStatus:          "",
			verifySCMStartedMissing: false,
			expectedSCMStartedVal:   "true",
		},
		{
			name:                    "zero concurrency limit - treated as no limit",
			concurrencyLimit:        intPtr(0),
			expectedState:           kubeinteraction.StateStarted,
			expectedStatus:          "",
			verifySCMStartedMissing: false,
			expectedSCMStartedVal:   "true",
		},
		{
			name:                    "positive concurrency limit - sets pending",
			concurrencyLimit:        intPtr(1),
			expectedState:           kubeinteraction.StateQueued,
			expectedStatus:          pipelinev1.PipelineRunSpecStatusPending,
			verifySCMStartedMissing: true,
		},
		{
			name:                    "higher concurrency limit - still sets pending",
			concurrencyLimit:        intPtr(5),
			expectedState:           kubeinteraction.StateQueued,
			expectedStatus:          pipelinev1.PipelineRunSpecStatusPending,
			verifySCMStartedMissing: true,
		},
		{
			name:                    "negative concurrency limit - treated as having limit (queued)",
			concurrencyLimit:        intPtr(-1),
			expectedState:           kubeinteraction.StateQueued,
			expectedStatus:          pipelinev1.PipelineRunSpecStatusPending,
			verifySCMStartedMissing: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupStartPRTestDefault(t)
			defer fixture.teardown()

			match := createTestMatch(tt.concurrencyLimit)

			pr, err := fixture.pac.startPR(fixture.ctx, match)

			assert.NilError(t, err)
			assert.Assert(t, pr != nil)

			state, hasState := pr.GetAnnotations()[keys.State]
			assert.Assert(t, hasState, "State annotation should be set")
			assert.Equal(t, state, tt.expectedState)

			assert.Equal(t, pr.Spec.Status, tt.expectedStatus)

			scmStarted, hasStarted := pr.GetAnnotations()[keys.SCMReportingPLRStarted]
			if tt.verifySCMStartedMissing {
				assert.Assert(t, !hasStarted, "SCMReportingPLRStarted should NOT be set for queued PRs")
			} else {
				assert.Assert(t, hasStarted, "SCMReportingPLRStarted should be set for started PRs")
				assert.Equal(t, scmStarted, tt.expectedSCMStartedVal, "SCMReportingPLRStarted should have correct value")
			}
		})
	}
}

func TestStartPR_StatusCreationFailure(t *testing.T) {
	config := defaultStartPRTestConfig()
	config.createStatusErorring = true
	fixture := setupStartPRTestWithConfig(t, config)
	defer fixture.teardown()

	match := createTestMatch(nil)

	pr, err := fixture.pac.startPR(fixture.ctx, match)

	// When CreateStatus fails, the PR should still be returned along with the error
	// This allows the caller to handle the situation and avoid nil pointer dereferences
	assert.Assert(t, pr != nil, "PipelineRun should be returned even when CreateStatus fails")
	assert.ErrorContains(t, err, "cannot use the API on the provider platform")
	assert.ErrorContains(t, err, "in_progress status")
}

func TestStartPR_GitHubAppLogURLHandling(t *testing.T) {
	fixture := setupStartPRTestDefault(t)
	defer fixture.teardown()

	match := createTestMatch(nil)

	// Add InstallationID annotation to simulate GitHub App
	match.PipelineRun.Annotations[keys.InstallationID] = "12345"

	pr, err := fixture.pac.startPR(fixture.ctx, match)

	assert.NilError(t, err)
	assert.Assert(t, pr != nil)

	// Verify InstallationID annotation is preserved
	installationID, hasInstallationID := pr.GetAnnotations()[keys.InstallationID]
	assert.Assert(t, hasInstallationID, "InstallationID annotation should be present")
	assert.Equal(t, installationID, "12345")

	// Verify state is set correctly
	state, hasState := pr.GetAnnotations()[keys.State]
	assert.Assert(t, hasState, "State annotation should be set")
	assert.Assert(t, state == kubeinteraction.StateStarted || state == kubeinteraction.StateQueued)
}

func TestStartPR_PatchBehavior(t *testing.T) {
	tests := []struct {
		name                string
		simulatePatchError  bool
		patchErrorMsg       string
		expectError         bool
		expectErrorContains []string
		expectedLogSnippet  string
		verifyAnnotations   bool // whether to verify annotations were set correctly
	}{
		{
			name:              "successful patch - all annotations set",
			verifyAnnotations: true,
		},
		{
			name:               "patch failure - PR still returned with error",
			simulatePatchError: true,
			patchErrorMsg:      "etcd unavailable",
			expectedLogSnippet: "cannot patch pipelinerun",
			verifyAnnotations:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			if tt.simulatePatchError {
				// Need custom reactor to simulate patch failure
				ctx, _ := rtesting.SetupFakeContext(t)

				stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
					Namespaces: []*corev1.Namespace{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-namespace",
							},
						},
					},
				})

				// Add reactor to simulate patch failure
				patchAttempts := 0
				stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					patchAttempts++
					if patchAttempts == 1 {
						return true, nil, fmt.Errorf("%s", tt.patchErrorMsg)
					}
					return false, nil, nil
				})

				cs := &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Log:            logger,
						Kube:           stdata.Kube,
						Tekton:         stdata.Pipeline,
					},
					Info: info.Info{
						Controller: &info.ControllerInfo{
							Name:      "default",
							Configmap: "pipelines-as-code",
							Secret:    "pipelines-as-code-secret",
						},
					},
				}
				cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

				event := &info.Event{
					SHA:               "test-sha",
					Organization:      "test-org",
					Repository:        "test-repo",
					URL:               "https://test.com/repo",
					HeadBranch:        "test-branch",
					BaseBranch:        "main",
					Sender:            "test-user",
					EventType:         "pull_request",
					TriggerTarget:     "pull_request",
					PullRequestNumber: 123,
					Provider: &info.Provider{
						Token: "test-token",
						User:  "git",
						URL:   "https://api.github.com",
					},
				}

				fakeclient, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
				defer teardown()

				// Setup GitHub API mock responses
				replyString(mux, fmt.Sprintf("/repos/%s/%s/statuses/%s", event.Organization, event.Repository, event.SHA), "{}")
				replyString(mux, fmt.Sprintf("/repos/%s/%s/check-runs", event.Organization, event.Repository), `{"id": 123}`)
				event.Provider.URL = ghTestServerURL

				kint := &kitesthelper.KinterfaceTest{
					ConsoleURL: "https://console.test",
				}

				pacInfo := &info.PacOpts{
					Settings: settings.Settings{
						SecretAutoCreation: true,
					},
				}

				vcx := setupProviderForTest(cs, logger, fakeclient, pacInfo)
				p := NewPacs(event, vcx, cs, pacInfo, kint, logger, nil)

				match := createTestMatch(nil)
				pr, err := p.startPR(ctx, match)

				assert.Assert(t, pr != nil, "PipelineRun should be returned even when patch fails")
				if tt.expectError {
					assert.Assert(t, err != nil, "Error should be returned")
					for _, errMsg := range tt.expectErrorContains {
						assert.ErrorContains(t, err, errMsg)
					}
				}
				assert.Equal(t, pr.GetNamespace(), "test-namespace")
				assert.Assert(t, patchAttempts >= 1, "Patch should have been attempted")
			} else {
				fixture := setupStartPRTestDefault(t)
				defer fixture.teardown()

				match := createTestMatch(nil)
				pr, err := fixture.pac.startPR(fixture.ctx, match)

				assert.NilError(t, err)
				assert.Assert(t, pr != nil, "PipelineRun should be returned")

				if tt.expectedLogSnippet != "" {
					logmsg := log.FilterMessageSnippet(tt.expectedLogSnippet).TakeAll()
					assert.Assert(t, len(logmsg) > 0, "log messages", logmsg, tt.expectedLogSnippet)
				}

				if tt.verifyAnnotations {
					state, hasState := pr.GetAnnotations()[keys.State]
					assert.Assert(t, hasState, "State annotation should be patched")
					assert.Assert(t, state == kubeinteraction.StateStarted || state == kubeinteraction.StateQueued,
						"State should be either started or queued")

					logURL, hasLogURL := pr.GetAnnotations()[keys.LogURL]
					assert.Assert(t, hasLogURL, "LogURL annotation should be patched")
					assert.Assert(t, logURL != "", "LogURL should not be empty")
				}
			}
		})
	}
}

// startPRResult holds the result of a concurrent startPR call.
type startPRResult struct {
	pr  *pipelinev1.PipelineRun
	err error
	idx int
}

// runConcurrentStartPR executes multiple startPR calls concurrently using a synchronization barrier
// to ensure they all start at the same time. It returns a channel of results.
func runConcurrentStartPR(t *testing.T, count int, fn func(idx int) (*pipelinev1.PipelineRun, error)) chan startPRResult {
	t.Helper()

	results := make(chan startPRResult, count)
	ready := make(chan struct{}, count)
	start := make(chan struct{})

	for i := range count {
		go func(idx int) {
			// Signal ready and wait for start signal to ensure concurrent execution
			ready <- struct{}{}
			<-start

			pr, err := fn(idx)
			results <- startPRResult{pr: pr, err: err, idx: idx}
		}(i)
	}

	// Wait for all goroutines to be ready
	for range count {
		<-ready
	}

	// Start all goroutines simultaneously
	close(start)

	return results
}

func TestStartPR_ConcurrentCreation(t *testing.T) {
	cs, event, logger, ctx, fakeclient, teardown := setupStartPRTest(t)
	defer teardown()

	kint := &kitesthelper.KinterfaceTest{
		ConsoleURL: "https://console.test",
	}

	pacInfo := &info.PacOpts{
		Settings: settings.Settings{
			SecretAutoCreation: true,
		},
	}

	vcx := setupProviderForTest(cs, logger, fakeclient, pacInfo)

	// Create multiple matches with different names
	numConcurrent := 5
	matches := make([]matcher.Match, numConcurrent)
	for i := range numConcurrent {
		matches[i] = createTestMatch(nil)
		// Use actual names instead of GenerateName for fake client compatibility
		matches[i].PipelineRun.Name = fmt.Sprintf("test-pr-%d", i)
		matches[i].PipelineRun.GenerateName = ""
		// Use different secret names to avoid conflicts
		matches[i].PipelineRun.Annotations[keys.GitAuthSecret] = fmt.Sprintf("test-git-secret-%d", i)
	}

	results := runConcurrentStartPR(t, numConcurrent, func(idx int) (*pipelinev1.PipelineRun, error) {
		p := NewPacs(event, vcx, cs, pacInfo, kint, logger, nil)
		return p.startPR(ctx, matches[idx])
	})

	successCount := 0
	failureCount := 0
	for range numConcurrent {
		res := <-results
		if res.err == nil && res.pr != nil {
			successCount++
			assert.Assert(t, res.pr.GetName() != "", "PipelineRun should have a name")
			assert.Equal(t, res.pr.GetNamespace(), "test-namespace")

			_, hasState := res.pr.GetAnnotations()[keys.State]
			assert.Assert(t, hasState, fmt.Sprintf("State annotation should be set for PR %d", res.idx))

			secretName := res.pr.GetAnnotations()[keys.GitAuthSecret]
			assert.Equal(t, secretName, fmt.Sprintf("test-git-secret-%d", res.idx))
		} else if res.err != nil {
			failureCount++
			t.Logf("PipelineRun %d creation failed: %v", res.idx, res.err)
		}
	}

	// All should succeed with proper isolation (each has unique name and secret)
	assert.Equal(t, successCount, numConcurrent, "All concurrent PipelineRuns should succeed with proper isolation, got %d/%d (failures: %d)", successCount, numConcurrent, failureCount)
	t.Logf("Successfully created %d/%d concurrent PipelineRuns", successCount, numConcurrent)
}
