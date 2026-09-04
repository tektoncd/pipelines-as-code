package reconciler

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v90/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	testkubernetestint "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	knativeapi "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
	"knative.dev/pkg/system"
)

var (
	randomURL          = "https://github.com/random/app"
	finalSuccessStatus = "success"
	finalFailureStatus = "failure"
)

func TestCopyRepositoryForMergeCopiesMutableSpecPointers(t *testing.T) {
	assert.Assert(t, copyRepositoryForMerge(nil) == nil)

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo",
			Namespace: "ns",
		},
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"org/repo"},
				Policy: &v1alpha1.Policy{
					OkToTest:    []string{"alice"},
					PullRequest: []string{"bob"},
				},
				Gitlab: &v1alpha1.GitlabSettings{
					CommentStrategy: "update",
				},
				Github: &v1alpha1.GithubSettings{
					CommentStrategy: "update",
				},
				Forgejo: &v1alpha1.ForgejoSettings{
					UserAgent: "pac-test",
				},
				AIAnalysis: &v1alpha1.AIAnalysisConfig{
					Enabled: true,
				},
			},
			GitProvider: &v1alpha1.GitProvider{
				Secret:        &v1alpha1.Secret{Name: "provider-secret"},
				WebhookSecret: &v1alpha1.Secret{Name: "webhook-secret"},
			},
		},
	}

	copied := copyRepositoryForMerge(repo)
	assert.Assert(t, copied != repo)
	assert.Assert(t, copied.Spec.Settings != repo.Spec.Settings)
	assert.Assert(t, copied.Spec.Settings.Policy != repo.Spec.Settings.Policy)
	assert.Assert(t, copied.Spec.Settings.Gitlab != repo.Spec.Settings.Gitlab)
	assert.Assert(t, copied.Spec.Settings.Github != repo.Spec.Settings.Github)
	assert.Assert(t, copied.Spec.Settings.Forgejo != repo.Spec.Settings.Forgejo)
	assert.Assert(t, copied.Spec.Settings.AIAnalysis != repo.Spec.Settings.AIAnalysis)
	assert.Assert(t, copied.Spec.GitProvider != repo.Spec.GitProvider)
	assert.Assert(t, copied.Spec.GitProvider.Secret != repo.Spec.GitProvider.Secret)
	assert.Assert(t, copied.Spec.GitProvider.WebhookSecret != repo.Spec.GitProvider.WebhookSecret)

	copied.Spec.Settings.GithubAppTokenScopeRepos[0] = "changed/repo"
	copied.Spec.Settings.Policy.OkToTest[0] = "mallory"
	copied.Spec.Settings.Policy.PullRequest[0] = "eve"
	copied.Spec.GitProvider.Secret.Name = "changed-provider-secret"
	copied.Spec.GitProvider.WebhookSecret.Name = "changed-webhook-secret"

	assert.Equal(t, "org/repo", repo.Spec.Settings.GithubAppTokenScopeRepos[0])
	assert.Equal(t, "alice", repo.Spec.Settings.Policy.OkToTest[0])
	assert.Equal(t, "bob", repo.Spec.Settings.Policy.PullRequest[0])
	assert.Equal(t, "provider-secret", repo.Spec.GitProvider.Secret.Name)
	assert.Equal(t, "webhook-secret", repo.Spec.GitProvider.WebhookSecret.Name)
}

func testSetupGHReplies(t *testing.T, mux *http.ServeMux, runevent *info.Event, checkrunID, finalStatus string) {
	t.Helper()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/check-runs/%s", runevent.Organization, runevent.Repository, checkrunID),
		func(_ http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			created := github.CreateCheckRunOptions{}
			err := json.Unmarshal(body, &created)
			assert.NilError(t, err)
			if created.GetStatus() == "completed" {
				assert.Equal(t, created.GetConclusion(), finalStatus, "we got the status `%s` but we should have get the status `%s`", created.GetConclusion(), finalStatus)
				golden.Assert(t, created.GetOutput().GetText(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			}
		})
}

func TestReconcilerReconcileKind(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	vcx := &ghprovider.Provider{
		Token:  github.Ptr("None"),
		Logger: fakelogger,
	}

	vcx.SetGithubClient(fakeclient)

	tests := []struct {
		name          string
		finalStatus   string
		checkRunID    string
		exitCode      int32
		taskCondition knativeduckv1.Conditions
	}{
		{
			name:        "success pipelinerun",
			checkRunID:  "6566930541",
			finalStatus: finalSuccessStatus,
			exitCode:    0,
			taskCondition: knativeduckv1.Conditions{
				{
					Type:   knativeapi.ConditionSucceeded,
					Status: corev1.ConditionTrue,
					Reason: string(tektonv1.PipelineRunReasonSuccessful),
				},
			},
		},
		{
			name:        "failed pipelinerun",
			finalStatus: finalFailureStatus,
			exitCode:    1,
			checkRunID:  "6566930542",
			taskCondition: knativeduckv1.Conditions{
				{
					Type:   knativeapi.ConditionSucceeded,
					Status: corev1.ConditionFalse,
					Reason: string(tektonv1.PipelineRunReasonFailed),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			clock := clockwork.NewFakeClock()
			statusPR := tektonv1.PipelineRunReasonSuccessful
			if tt.finalStatus == finalFailureStatus {
				statusPR = tektonv1.PipelineRunReasonFailed
			}
			pr := tektontest.MakePRCompletion(clock, "pipeline-newest", "ns", string(statusPR), nil, make(map[string]string), 10)
			pr.Status.ChildReferences = []tektonv1.ChildStatusReference{
				{
					TypeMeta: runtime.TypeMeta{
						Kind: "TaskRun",
					},
					Name:             "task1",
					PipelineTaskName: "task1",
				},
			}

			secretName := secrets.GenerateBasicAuthSecretName()
			ctx = info.StoreCurrentControllerName(ctx, "default")

			pr.Annotations = map[string]string{
				keys.GitAuthSecret:  secretName,
				keys.State:          kubeinteraction.StateCompleted,
				keys.InstallationID: "1234",
				keys.RepoURL:        randomURL,
				keys.Repository:     pr.GetName(),
				keys.OriginalPRName: pr.GetName(),
				keys.CheckRunID:     tt.checkRunID,
				keys.URLOrg:         "random",
				keys.URLRepository:  "app",
			}
			pr.Labels = map[string]string{
				keys.Repository: pr.GetName(),
			}

			testRepo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pr.GetName(),
					Namespace: pr.GetNamespace(),
				},
				Spec: v1alpha1.RepositorySpec{
					URL: randomURL,
					GitProvider: &v1alpha1.GitProvider{
						URL: "https://another.example.com",
					},
				},
			}
			globalRepo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "global-repo",
					Namespace: "ns",
				},
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						PipelineRunProvenance: "default_branch",
					},
					GitProvider: &v1alpha1.GitProvider{
						URL: "https://github.com",
						Secret: &v1alpha1.Secret{
							Name: "global-provider-secret",
						},
					},
				},
			}

			taskStatus := tektonv1.TaskRunStatusFields{
				PodName: "task1",
				Steps: []tektonv1.StepState{
					{
						Name: "step1",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: tt.exitCode,
							},
						},
					},
				},
			}
			testData := testclient.Data{
				Repositories: []*v1alpha1.Repository{testRepo, globalRepo},
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				TaskRuns: []*tektonv1.TaskRun{
					tektontest.MakeTaskRunCompletion(clock, "task1", "ns", "pipeline-newest",
						map[string]string{}, taskStatus, tt.taskCondition, 10),
				},
				Secret: []*corev1.Secret{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: testRepo.Namespace,
							Name:      secretName,
						},
					},
				},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)

			metrics, err := prmetrics.NewRecorder()
			assert.NilError(t, err)

			r := Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         queuepkg.NewManager(fakelogger),
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
					},
					Info: info.Info{
						Kube: &info.KubeOpts{
							Namespace: "ns",
						},
						Controller: &info.ControllerInfo{
							Secret:           secretName,
							GlobalRepository: "global-repo",
						},
					},
				},
				pipelineRunLister: stdata.PipelineLister,
				kinteract: &kubeinteraction.Interaction{
					Run: &params.Run{
						Clients: clients.Clients{
							Kube:   stdata.Kube,
							Tekton: stdata.Pipeline,
						},
					},
				},
				metrics: metrics,
			}
			r.run.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					ErrorLogSnippet:    true,
					SecretAutoCreation: true,
				},
			}
			vcx.SetPacInfo(pacInfo)

			event := buildEventFromPipelineRun(pr)
			testSetupGHReplies(t, mux, event, tt.checkRunID, tt.finalStatus)

			_, err = r.reportFinalStatus(ctx, fakelogger, pacInfo, event, pr, vcx)
			assert.NilError(t, err)

			got, err := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
			assert.NilError(t, err)

			// state must be updated to completed
			assert.Equal(t, got.Annotations[keys.State], kubeinteraction.StateCompleted)
			cachedRepo, err := informers.Repository.Lister().Repositories(testRepo.Namespace).Get(testRepo.Name)
			assert.NilError(t, err)
			assert.Assert(t, cachedRepo.Spec.Settings == nil, "global settings should not mutate the cached Repository")
			assert.Assert(t, cachedRepo.Spec.GitProvider.Secret == nil, "global secret should not mutate the cached Repository")
		})
	}
}

func TestInitGitProviderClientUsesGlobalSecretWithoutMutatingCache(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
			Annotations: map[string]string{
				keys.GitProvider:   "github",
				keys.RepoURL:       "https://github.com/org/repo",
				keys.URLOrg:        "org",
				keys.URLRepository: "repo",
				keys.SHA:           "abc123",
			},
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			URL:         "https://github.com/org/repo",
			GitProvider: &v1alpha1.GitProvider{},
		},
	}
	globalRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-repo",
			Namespace: "global",
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "global-provider-secret",
				},
			},
		},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo, globalRepo},
		ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
	})
	ctx = info.StoreNS(ctx, system.Namespace())
	kint := &testkubernetestint.KinterfaceTest{
		GetSecretResult: map[string]string{
			"global-provider-secret": "test-token",
		},
	}
	r := &Reconciler{
		repoLister:   informers.Repository.Lister(),
		kinteract:    kint,
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
		run: &params.Run{
			Clients: clients.Clients{
				Kube:           stdata.Kube,
				PipelineAsCode: stdata.PipelineAsCode,
				Log:            logger,
			},
			Info: info.Info{
				Kube: &info.KubeOpts{
					Namespace: "global",
				},
				Controller: &info.ControllerInfo{
					GlobalRepository: "global-repo",
				},
				Pac: info.NewPacOpts(),
			},
		},
	}

	cachedRepo, err := informers.Repository.Lister().Repositories(repo.Namespace).Get(repo.Name)
	assert.NilError(t, err)
	_, event, err := r.initGitProviderClient(ctx, logger, cachedRepo, pr)
	assert.NilError(t, err)
	assert.Equal(t, "test-token", event.Provider.Token)

	cachedRepo, err = informers.Repository.Lister().Repositories(repo.Namespace).Get(repo.Name)
	assert.NilError(t, err)
	assert.Assert(t, cachedRepo.Spec.GitProvider.Secret == nil, "global secret should not mutate the cached Repository")
}

func TestUpdatePipelineRunState(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	tests := []struct {
		name          string
		pipelineRun   *tektonv1.PipelineRun
		state         string
		wantSpecPatch bool
	}{
		{
			name: "queued to started",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateQueued,
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
				Status: tektonv1.PipelineRunStatus{},
			},
			state:         kubeinteraction.StateStarted,
			wantSpecPatch: true,
		},
		{
			name: "started to completed",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateStarted,
					},
				},
				Spec:   tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{},
			},
			state: kubeinteraction.StateCompleted,
		},
		{
			name: "already running reported as started should not repatch spec",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateQueued,
					},
				},
				// Tekton already cleared the pending status and started the
				// PipelineRun before PAC got a chance to report it (see the
				// "Running reason without SCMReportingPLRStarted" scenario in
				// TestReconcileKindSCMReportingLogic).
				Spec:   tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{},
			},
			state:         kubeinteraction.StateStarted,
			wantSpecPatch: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{tt.pipelineRun},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)

			var gotPatch []byte
			stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if patch, ok := action.(k8stesting.PatchAction); ok {
					gotPatch = patch.GetPatch()
				}
				return false, nil, nil
			})

			r := &Reconciler{
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
					},
				},
			}

			updatedPR, err := r.updatePipelineRunState(ctx, fakelogger, tt.pipelineRun, tt.state)
			assert.NilError(t, err)

			assert.Equal(t, updatedPR.Annotations[keys.State], tt.state)
			assert.Equal(t, updatedPR.Spec.Status, tektonv1.PipelineRunSpecStatus(""))

			var patchBody map[string]any
			assert.NilError(t, json.Unmarshal(gotPatch, &patchBody))
			_, hasSpec := patchBody["spec"]
			assert.Equal(t, hasSpec, tt.wantSpecPatch, "unexpected spec field in patch %s", string(gotPatch))

			// Test SCMReportingPLRStarted annotation for started state
			if tt.state == kubeinteraction.StateStarted {
				scmStarted, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				assert.Assert(t, exists, "SCMReportingPLRStarted annotation should exist when state is started")
				assert.Equal(t, scmStarted, "true", "SCMReportingPLRStarted should be 'true'")
			} else {
				_, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				assert.Assert(t, !exists, "SCMReportingPLRStarted annotation should not exist for non-started states")
			}
		})
	}
}

func TestReconcileKindControllerInfoHandling(t *testing.T) {
	tests := []struct {
		name        string
		annotation  string
		wantErrSub  string
		wantControl *info.ControllerInfo
	}{
		{
			name:       "invalid controller annotation",
			annotation: "{",
			wantErrSub: "failed to parse controllerInfo",
		},
		{
			name:       "null controller annotation",
			annotation: "null",
			wantErrSub: "value must not be null",
		},
		{
			name:        "controller annotation does not mutate shared run",
			annotation:  `{"name":"secondary","configmap":"secondary-config","secret":"secondary-secret","gRepo":"secondary-global"}`,
			wantControl: &info.ControllerInfo{Name: "default", Secret: "default-secret", GlobalRepository: "default-global"},
		},
		{
			name:        "fallback controller is copied",
			wantControl: &info.ControllerInfo{Name: "default", Secret: "default-secret", GlobalRepository: "default-global"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			controller := &info.ControllerInfo{Name: "default", Secret: "default-secret", GlobalRepository: "default-global"}
			annotations := map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "true",
			}
			if tt.annotation != "" {
				annotations[keys.ControllerInfo] = tt.annotation
			}
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pr",
					Namespace:   "test-ns",
					Annotations: annotations,
				},
			}
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				Repositories: []*v1alpha1.Repository{repo},
			})
			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
						Log:    logger,
					},
					Info: info.Info{
						Pac: &info.PacOpts{
							Settings: settings.Settings{},
						},
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: controller,
					},
				},
			}

			err := r.ReconcileKind(ctx, pr)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
			}
			if tt.wantControl != nil {
				assert.DeepEqual(t, r.run.Info.Controller, tt.wantControl)
				assert.Assert(t, r.run.Info.Controller == controller, "reconcile must keep the shared controller pointer")
			}
		})
	}
}

// TestReconcileKindReturnsErrorWhenDetectProviderFailsTransiently asserts that
// a done PipelineRun whose git-provider annotation names a real provider, but
// whose provider client fails to initialize for what could be a transient
// reason (here: the GitHub App private key secret is missing), makes
// ReconcileKind return an error instead of nil. Swallowing the error here
// used to forget the workqueue key: reportFinalStatus never ran, the
// finished PipelineRun's concurrency slot was never released, and nothing
// ever retried.
func TestReconcileKindReturnsErrorWhenDetectProviderFailsTransiently(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
			Annotations: map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "true",
				keys.GitProvider:   "github",
				// A non-zero InstallationID routes detectProvider through
				// InitAppClient, which fails here because no GitHub App
				// private key secret was seeded -- a stand-in for any
				// transient client-initialization failure. CheckRunID must
				// also be set, or reconcileKind returns early before ever
				// reaching detectProvider.
				keys.InstallationID: "123",
				keys.CheckRunID:     "456",
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: knativeduckv1.Status{
				Conditions: knativeduckv1.Conditions{
					{
						Type:   knativeapi.ConditionSucceeded,
						Status: corev1.ConditionTrue,
						Reason: string(tektonv1.PipelineRunReasonSuccessful),
					},
				},
			},
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pr},
		Repositories: []*v1alpha1.Repository{repo},
	})
	metrics, err := prmetrics.NewRecorder()
	assert.NilError(t, err)
	r := &Reconciler{
		repoLister: informers.Repository.Lister(),
		qm:         queuepkg.NewManager(logger),
		run: &params.Run{
			Clients: clients.Clients{
				Tekton: stdata.Pipeline,
				Kube:   stdata.Kube,
				Log:    logger,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{},
				},
				Kube: &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{
					Name:             "default",
					Secret:           "default-secret",
					GlobalRepository: "default-global",
				},
			},
		},
		metrics:      metrics,
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
	}

	err = r.ReconcileKind(ctx, pr)
	assert.ErrorContains(t, err, "detect provider")
	assert.Assert(t, !stderrors.Is(err, ErrProviderNotConfigured),
		"a transient client-init failure must not be classified as permanent")
}

// TestReconcileKindAbandonsDoneRunWhenProviderNotConfigured asserts that a
// done PipelineRun whose git-provider annotation can never be resolved (a
// permanent condition: retrying detectProvider can never succeed, whether
// the annotation is missing or names an unsupported provider) is marked
// failed and has its concurrency slot released and handed to the next
// queued candidate, and that ReconcileKind returns nil rather than an error
// that would otherwise be retried forever while still holding the slot.
func TestReconcileKindAbandonsDoneRunWhenProviderNotConfigured(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
	}{
		{
			name: "missing git-provider annotation",
			annotations: map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "true",
			},
		},
		{
			name: "unknown git-provider annotation",
			annotations: map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "true",
				keys.GitProvider:   "batman",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pr",
					Namespace:   "test-ns",
					Annotations: tt.annotations,
				},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionTrue,
								Reason: string(tektonv1.PipelineRunReasonSuccessful),
							},
						},
					},
				},
			}
			nextPR := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "next-pr",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateQueued,
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
			}
			limit := 1
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.RepositorySpec{
					ConcurrencyLimit: &limit,
				},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr, nextPR},
				Repositories: []*v1alpha1.Repository{repo},
			})
			metrics, err := prmetrics.NewRecorder()
			assert.NilError(t, err)

			qm := queuepkg.NewManager(logger)
			_, err = qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(pr)})
			assert.NilError(t, err)
			assert.DeepEqual(t, qm.RunningPipelineRuns(repo), []string{queuepkg.PrKey(pr)})
			assert.NilError(t, qm.AddToPendingQueue(repo, []string{queuepkg.PrKey(nextPR)}))

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         qm,
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
						Kube:   stdata.Kube,
						Log:    logger,
					},
					Info: info.Info{
						Pac: &info.PacOpts{
							Settings: settings.Settings{},
						},
						Kube: &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{
							Name:             "default",
							Secret:           "default-secret",
							GlobalRepository: "default-global",
						},
					},
				},
				metrics:      metrics,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			err = r.ReconcileKind(ctx, pr)
			assert.NilError(t, err, "a permanent detectProvider failure must not be retried forever")
			assert.DeepEqual(t, qm.RunningPipelineRuns(repo), []string{queuepkg.PrKey(nextPR)})
			assert.Assert(t, len(qm.QueuedPipelineRuns(repo)) == 0, "the promoted candidate must no longer be pending")

			updated, err := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, updated.GetAnnotations()[keys.State], kubeinteraction.StateFailed)
		})
	}
}

// TestReconcileKindAbandonsDoneRunReleasesQueueEvenWhenStatePatchFails proves
// the ordering in abandonDoneWithoutProvider: the concurrency slot must be
// released before the terminal state is written, not after. If the state
// patch fails (here: the PipelineRun was deleted from the fake client
// between detectProvider failing and the patch), ReconcileKind must still
// return an error (so this reconcile is retried), but the queue release must
// already have happened -- a retry that also fails to patch state must not
// re-promote a second candidate, since RemoveAndTakeItemFromQueue is a no-op
// once the reservation is gone.
func TestReconcileKindAbandonsDoneRunReleasesQueueEvenWhenStatePatchFails(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
			Annotations: map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "true",
				// keys.GitProvider deliberately absent: detectProvider fails
				// permanently.
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: knativeduckv1.Status{
				Conditions: knativeduckv1.Conditions{
					{
						Type:   knativeapi.ConditionSucceeded,
						Status: corev1.ConditionTrue,
						Reason: string(tektonv1.PipelineRunReasonSuccessful),
					},
				},
			},
		},
	}
	limit := 1
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: &limit,
		},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pr},
		Repositories: []*v1alpha1.Repository{repo},
	})
	metrics, err := prmetrics.NewRecorder()
	assert.NilError(t, err)

	qm := queuepkg.NewManager(logger)
	_, err = qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(pr)})
	assert.NilError(t, err)

	// Make the terminal-state patch fail while leaving reads (including the
	// "fetch latest" Get at the top of reconcileKind) unaffected, so the
	// failure is isolated to abandonDoneWithoutProvider's state write.
	stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("injected patch failure")
	})

	r := &Reconciler{
		repoLister: informers.Repository.Lister(),
		qm:         qm,
		run: &params.Run{
			Clients: clients.Clients{
				Tekton: stdata.Pipeline,
				Kube:   stdata.Kube,
				Log:    logger,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{},
				},
				Kube: &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{
					Name:             "default",
					Secret:           "default-secret",
					GlobalRepository: "default-global",
				},
			},
		},
		metrics:      metrics,
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
	}

	err = r.ReconcileKind(ctx, pr)
	assert.ErrorContains(t, err, "cannot update state")
	assert.Assert(t, len(qm.RunningPipelineRuns(repo)) == 0, "the concurrency slot must already be released even though the state patch failed")

	// A second attempt (simulating a retry after the failed patch) must not
	// find another reservation to release or promote: the queue step is
	// already done and must not run twice.
	r.startNextPipelineRunInQueue(ctx, logger, repo, pr)
	assert.Assert(t, len(qm.RunningPipelineRuns(repo)) == 0, "retrying the queue release must stay a no-op")
}

func TestReconcileKindSCMReportingLogic(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	_, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
	defer teardown()

	// Mock status endpoint
	mux.HandleFunc("/repos/random/app/statuses/123afc", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"state":"pending"}`)
	})

	tests := []struct {
		name                         string
		pipelineRun                  *tektonv1.PipelineRun
		shouldCallUpdateToInProgress bool
		description                  string
	}{
		{
			name: "Running reason without SCMReportingPLRStarted - should call updatePipelineRunToInProgress",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test-pr",
					Annotations: map[string]string{
						keys.State:         kubeinteraction.StateQueued,
						keys.Repository:    "test-repo",
						keys.GitProvider:   "github",
						keys.SHA:           "123afc",
						keys.URLOrg:        "random",
						keys.URLRepository: "app",
						keys.SecretCreated: "true",
					},
				},
				Spec: tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionUnknown,
								Reason: string(tektonv1.PipelineRunReasonRunning),
							},
						},
					},
				},
			},
			shouldCallUpdateToInProgress: true,
			description:                  "PipelineRun is Running but no SCM reporting done yet",
		},
		{
			name: "Running reason with SCMReportingPLRStarted=true - should NOT call updatePipelineRunToInProgress",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test-pr",
					Annotations: map[string]string{
						keys.State:                  kubeinteraction.StateStarted,
						keys.Repository:             "test-repo",
						keys.SCMReportingPLRStarted: "true",
						keys.GitProvider:            "github",
						keys.SHA:                    "123afc",
						keys.URLOrg:                 "random",
						keys.URLRepository:          "app",
						keys.SecretCreated:          "true",
					},
				},
				Spec: tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionUnknown,
								Reason: string(tektonv1.PipelineRunReasonRunning),
							},
						},
					},
				},
			},
			shouldCallUpdateToInProgress: false,
			description:                  "PipelineRun is Running and SCM reporting already done",
		},
		{
			name: "Non-Running reason - should NOT call updatePipelineRunToInProgress",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test-pr",
					Annotations: map[string]string{
						keys.State:         kubeinteraction.StateQueued,
						keys.Repository:    "test-repo",
						keys.GitProvider:   "github",
						keys.SHA:           "123afc",
						keys.URLOrg:        "random",
						keys.URLRepository: "app",
						keys.SecretCreated: "true",
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionUnknown,
								Reason: string(tektonv1.PipelineRunReasonPending),
							},
						},
					},
				},
			},
			shouldCallUpdateToInProgress: false,
			description:                  "PipelineRun is not in Running state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			testRepo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: tt.pipelineRun.GetNamespace(),
				},
				Spec: v1alpha1.RepositorySpec{
					URL: randomURL,
					GitProvider: &v1alpha1.GitProvider{
						URL: ghTestServerURL,
						Secret: &v1alpha1.Secret{
							Name: "pac-git-basic-auth-owner-repo",
						},
					},
				},
			}

			// The controller only sends credentials to hostnames its ConfigMap
			// trusts, so the test server has to be listed like any self hosted
			// instance would be.
			testServerHost := strings.TrimPrefix(ghTestServerURL, "http://")
			testData := testclient.Data{
				Repositories: []*v1alpha1.Repository{testRepo},
				PipelineRuns: []*tektonv1.PipelineRun{tt.pipelineRun},
				ConfigMap: []*corev1.ConfigMap{{
					ObjectMeta: metav1.ObjectMeta{Name: "pipelines-as-code", Namespace: system.Namespace()},
					Data:       map[string]string{settings.TrustedProviderHostnamesKey: testServerHost},
				}},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			ctx = info.StoreNS(ctx, system.Namespace())

			// Track if updatePipelineRunToInProgress was called by checking state changes
			originalState := tt.pipelineRun.GetAnnotations()[keys.State]

			kinterfaceTest := &testkubernetestint.KinterfaceTest{
				GetSecretResult: map[string]string{
					"pac-git-basic-auth-owner-repo": "https://whateveryousayboss",
				},
			}

			cs := &params.Run{
				Clients: clients.Clients{
					Tekton: stdata.Pipeline,
					Kube:   stdata.Kube,
					Log:    logger,
				},
				Info: info.Info{
					Pac: &info.PacOpts{
						Settings: settings.Settings{},
					},
					Kube: &info.KubeOpts{
						Namespace: "global",
					},
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				run:        cs,
				kinteract:  kinterfaceTest,
			}

			err := r.ReconcileKind(ctx, tt.pipelineRun)
			assert.Assert(t, cs.Info.Controller == nil, "reconciliation must not mutate shared controller state")

			// For test cases that should call updatePipelineRunToInProgress,
			// we expect no error and the state should be updated
			if tt.shouldCallUpdateToInProgress {
				assert.NilError(t, err, tt.description)

				// Get the updated PipelineRun to check if state was changed
				updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(tt.pipelineRun.GetNamespace()).Get(ctx, tt.pipelineRun.GetName(), metav1.GetOptions{})
				assert.NilError(t, getErr)

				// Should have been updated to started state with SCMReportingPLRStarted annotation
				assert.Equal(t, updatedPR.GetAnnotations()[keys.State], kubeinteraction.StateStarted)
				scmStarted, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				assert.Assert(t, exists, "SCMReportingPLRStarted should be set")
				assert.Equal(t, scmStarted, "true")
			} else {
				// For cases that should NOT call updatePipelineRunToInProgress,
				// the state should remain unchanged
				updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(tt.pipelineRun.GetNamespace()).Get(ctx, tt.pipelineRun.GetName(), metav1.GetOptions{})
				assert.NilError(t, getErr)

				// State should be unchanged
				assert.Equal(t, updatedPR.GetAnnotations()[keys.State], originalState, tt.description)

				// Check SCMReportingPLRStarted annotation based on original state
				scmStarted, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				if originalState == kubeinteraction.StateStarted {
					// If original state was already 'started', SCMReportingPLRStarted should exist and be "true"
					assert.Assert(t, exists, "SCMReportingPLRStarted should exist when original state is started")
					assert.Equal(t, scmStarted, "true", "SCMReportingPLRStarted should be 'true'")
				} else {
					// If original state was not 'started', SCMReportingPLRStarted should not exist
					assert.Assert(t, !exists, "SCMReportingPLRStarted should not exist when original state is not started")
				}
			}
		})
	}
}

func TestCreateSecretForPipelineRun(t *testing.T) {
	// Base annotations required by initGitProviderClient (detectProvider + buildEventFromPipelineRun)
	baseAnnotations := map[string]string{
		keys.GitProvider:   "github",
		keys.RepoURL:       "https://github.com/org/repo",
		keys.URLOrg:        "org",
		keys.URLRepository: "repo",
		keys.SHA:           "abc123",
	}

	providerSecretName := "pac-git-basic-auth-owner-repo"

	tests := []struct {
		name              string
		prAnnotations     map[string]string
		repoUser          string
		createSecretError error
		updateSecretError error
		simulatePatchErr  bool
		wantErr           string
		wantLogSnippet    string
		verifyPatched     bool
	}{
		{
			name:          "missing git-auth-secret annotation",
			prAnnotations: map[string]string{},
			wantErr:       "cannot get annotation",
		},
		{
			name: "MakeBasicAuthSecret failure with malformed URL",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
				keys.RepoURL:       "http://[invalid",
			},
			wantErr: "making basic auth secret",
		},
		{
			name: "CreateSecret generic failure",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			createSecretError: fmt.Errorf("connection timeout"),
			wantErr:           "creating basic auth secret",
		},
		{
			name: "CreateSecret AlreadyExists succeeds with warning",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			createSecretError: errors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "secrets"}, "test-secret"),
			wantLogSnippet:    "already exists",
			verifyPatched:     true,
		},
		{
			name: "UpdateSecretWithOwnerRef failure",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			updateSecretError: fmt.Errorf("failed to update owner ref"),
			wantErr:           "cannot update secret",
		},
		{
			name: "PatchPipelineRun failure returns error",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			simulatePatchErr: true,
			wantErr:          "failed to patch pipelinerun",
		},
		{
			name: "happy path with default git user",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			verifyPatched: true,
		},
		{
			name: "happy path with custom git user",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			repoUser:      "custom-user",
			verifyPatched: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			// Merge base annotations with test-specific annotations (test-specific overrides base)
			annotations := maps.Clone(baseAnnotations)
			maps.Copy(annotations, tt.prAnnotations)

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pr",
					Namespace:   "test-ns",
					Annotations: annotations,
				},
			}

			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.RepositorySpec{
					GitProvider: &v1alpha1.GitProvider{
						Secret: &v1alpha1.Secret{
							Name: providerSecretName,
						},
						User: tt.repoUser,
					},
				},
			}

			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				Repositories: []*v1alpha1.Repository{repo},
				ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			ctx = info.StoreNS(ctx, system.Namespace())

			if tt.simulatePatchErr {
				stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("etcd unavailable")
				})
			}

			kint := &testkubernetestint.KinterfaceTest{
				GetSecretResult: map[string]string{
					providerSecretName: "test-token",
				},
				CreateSecretError: tt.createSecretError,
				UpdateSecretError: tt.updateSecretError,
			}

			r := &Reconciler{
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
						Kube:   stdata.Kube,
						Log:    logger,
					},
					Info: info.Info{
						Pac: &info.PacOpts{
							Settings: settings.Settings{},
						},
						Kube: &info.KubeOpts{
							Namespace: "global",
						},
						Controller: &info.ControllerInfo{
							GlobalRepository: "global-repo",
						},
					},
				},
				repoLister:   informers.Repository.Lister(),
				kinteract:    kint,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			err := r.createSecretForPipelineRun(ctx, logger, pr, repo)

			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error containing: %s", tt.wantErr)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			if tt.wantLogSnippet != "" {
				logEntries := log.FilterMessageSnippet(tt.wantLogSnippet).TakeAll()
				assert.Assert(t, len(logEntries) > 0, "expected log snippet %q not found", tt.wantLogSnippet)
			}

			if tt.verifyPatched {
				updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
				assert.NilError(t, getErr)
				assert.Equal(t, updatedPR.Annotations[keys.SecretCreated], "true")
			}
		})
	}
}

func TestReconcileKindSecretCreationDoesNotLogOnSuccess(t *testing.T) {
	observer, log := zapobserver.New(zap.ErrorLevel)
	logger := zap.New(observer).Sugar()

	ctx, _ := rtesting.SetupFakeContext(t)

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-pr",
			Annotations: map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "false",
				keys.GitAuthSecret: "pac-git-basic-auth-owner-repo",
				keys.GitProvider:   "github",
				keys.RepoURL:       "https://github.com/org/repo",
				keys.URLOrg:        "org",
				keys.URLRepository: "repo",
				keys.SHA:           "abc123",
			},
		},
	}

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "pac-provider-secret",
				},
				User: "test-user",
			},
		},
	}

	testData := testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pr},
		Repositories: []*v1alpha1.Repository{repo},
		ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testData)
	ctx = info.StoreNS(ctx, system.Namespace())

	r := &Reconciler{
		repoLister: informers.Repository.Lister(),
		run: &params.Run{
			Clients: clients.Clients{
				Tekton: stdata.Pipeline,
				Kube:   stdata.Kube,
				Log:    logger,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{
						SecretAutoCreation: true,
					},
				},
				Kube: &info.KubeOpts{
					Namespace: "global",
				},
				Controller: &info.ControllerInfo{
					GlobalRepository: "global-repo",
				},
			},
		},
		kinteract: &testkubernetestint.KinterfaceTest{
			GetSecretResult: map[string]string{
				"pac-provider-secret": "test-token",
			},
		},
	}

	err := r.ReconcileKind(ctx, pr)
	assert.NilError(t, err)

	updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
	assert.NilError(t, getErr)
	assert.Equal(t, updatedPR.Annotations[keys.SecretCreated], "true")

	logEntries := log.FilterMessageSnippet("failed to create secret for pipelineRun").TakeAll()
	assert.Equal(t, len(logEntries), 0)
}

// defaultPolicyConfigMap is a controller ConfigMap with no configured allowlist,
// which is the state every stock install starts in: the public instances are
// trusted and nothing else is.
func defaultPolicyConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "pipelines-as-code", Namespace: system.Namespace()},
	}
}

// The watcher resolves the provider secret on its own, so the guard that stops a
// tenant Repository from inheriting the shared controller credential while
// pointing it somewhere else has to apply here too, not only on the adapter path.
func TestInitGitProviderClientRefusesInheritedSecretWithOwnProviderURL(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, system.Namespace())
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
			Annotations: map[string]string{
				keys.GitProvider:   "github",
				keys.RepoURL:       "https://github.com/org/repo",
				keys.URLOrg:        "org",
				keys.URLRepository: "repo",
				keys.SHA:           "abc123",
			},
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
		Spec: v1alpha1.RepositorySpec{
			URL:         "https://github.com/org/repo",
			GitProvider: &v1alpha1.GitProvider{URL: "https://gitea.com"},
		},
	}
	globalRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "global-repo", Namespace: "global"},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				URL:    "https://github.com",
				Secret: &v1alpha1.Secret{Name: "global-provider-secret"},
			},
		},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo, globalRepo},
		ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
	})
	r := &Reconciler{
		repoLister:   informers.Repository.Lister(),
		kinteract:    &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{"global-provider-secret": "test-token"}},
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
		run: &params.Run{
			Clients: clients.Clients{
				Kube:           stdata.Kube,
				PipelineAsCode: stdata.PipelineAsCode,
				Log:            logger,
			},
			Info: info.Info{
				Kube:       &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{GlobalRepository: "global-repo"},
				Pac:        info.NewPacOpts(),
			},
		},
	}

	cachedRepo, err := informers.Repository.Lister().Repositories(repo.Namespace).Get(repo.Name)
	assert.NilError(t, err)
	_, _, err = r.initGitProviderClient(ctx, logger, cachedRepo, pr)
	assert.ErrorContains(t, err, "must not be sent to an endpoint chosen by another namespace")
}
