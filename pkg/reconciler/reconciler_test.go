package reconciler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
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
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
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
	"knative.dev/pkg/logging"
	rtesting "knative.dev/pkg/reconciler/testing"
	"knative.dev/pkg/system"
)

const reconcilerFakePrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQC6GorZBeri0eVERMZQDFh5E1RMPjFk9AevaWr27yJse6eiUlos
gY2L2vcZKLOrdvVR+TLLapIMFfg1E1qVr1iTHP3IiSCs1uW6NKDmxEQc9Uf/fG9c
i56tGmTVxLkC94AvlVFmgxtWfHdP3lF2O0EcfRyIi6EIbGkWDqWQVEQG2wIDAQAB
AoGAaKOd6FK0dB5Si6Uj4ERgxosAvfHGMh4n6BAc7YUd1ONeKR2myBl77eQLRaEm
DMXRP+sfDVL5lUQRED62ky1JXlDc0TmdLiO+2YVyXI5Tbej0Q6wGVC25/HedguUX
fw+MdKe8jsOOXVRLrJ2GfpKZ2CmOKGTm/hyrFa10TmeoTxkCQQDa4fvqZYD4vOwZ
CplONnVk+PyQETj+mAyUiBnHEeLpztMImNLVwZbrmMHnBtCNx5We10oCLW+Qndfw
Xi4LgliVAkEA2amSV+TZiUVQmm5j9yzon0rt1FK+cmVWfRS/JAUXyvl+Xh/J+7Gu
QzoEGJNAnzkUIZuwhTfNRWlzURWYA8BVrwJAZFQhfJd6PomaTwAktU0REm9ulTrP
vSNE4PBhoHX6ZOGAqfgi7AgIfYVPm+3rupE5a82TBtx8vvUa/fqtcGkW4QJAaL9t
WPUeJyx/XMJxQzuOe1JA4CQt2LmiBLHeRoRY7ephgQSFXKYmed3KqNT8jWOXp5DY
Q1QWaigUQdpFfNCrqwJBANLgWaJV722PhQXOCmR+INvZ7ksIhJVcq/x1l2BYOLw2
QsncVExbMiPa9Oclo5qLuTosS8qwHm1MJEytp3/SkB8=
-----END RSA PRIVATE KEY-----`

var (
	randomURL          = "https://github.com/random/app"
	finalSuccessStatus = "success"
	finalFailureStatus = "failure"
)

type reportFinalStatusProvider struct {
	tprovider.TestProviderImp
	setClientErr error
}

func (v *reportFinalStatusProvider) SetClient(ctx context.Context, run *params.Run, event *info.Event, repo *v1alpha1.Repository, eventEmitter *events.EventEmitter) error {
	if v.setClientErr != nil {
		return v.setClientErr
	}
	return v.TestProviderImp.SetClient(ctx, run, event, repo, eventEmitter)
}

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
		Token:  new("None"),
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
				qm: queuepkg.NewManager(logger),
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
// already have happened. The manager remembers the handoff while the state
// write is retried, rather than acquiring another successor.
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
	assert.NilError(t, r.startNextPipelineRunInQueue(ctx, logger, repo, pr))
	assert.Assert(t, len(qm.RunningPipelineRuns(repo)) == 0, "retrying the queue release must stay a no-op")
}

// TestReconcileKindDeletedRepositoryWithDoneRun asserts that a PipelineRun
// whose Repository CR has been deleted stops being reconciled once it reached
// a terminal PAC state. Dropping the repository clears every queue entry it
// held, so there is nothing left to recover, and retrying could never make the
// deleted Repository reappear. A run that is not done keeps returning the
// error so the reconcile is retried.
func TestReconcileKindDeletedRepositoryWithDoneRun(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		wantErr bool
	}{
		{name: "completed run", state: kubeinteraction.StateCompleted},
		{name: "failed run", state: kubeinteraction.StateFailed},
		{name: "started run", state: kubeinteraction.StateStarted, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pr",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State:         tt.state,
						keys.Repository:    "deleted-repo",
						keys.SecretCreated: "true",
					},
				},
			}
			// Never seeded in the lister: the Repository CR is gone, but the
			// queue manager still holds the entries it had for it.
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "deleted-repo", Namespace: "test-ns"},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
			})
			qm := queuepkg.NewManager(logger)
			_, err := qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(pr)})
			assert.NilError(t, err)

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
						Pac:        &info.PacOpts{Settings: settings.Settings{}},
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{Name: "default", GlobalRepository: "default-global"},
					},
				},
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			err = r.ReconcileKind(ctx, pr)
			if tt.wantErr {
				assert.ErrorContains(t, err, "failed to get repository CR")
			} else {
				assert.NilError(t, err, "a done pipelineRun must not be retried forever for a deleted repository")
			}
			assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 0, "the queue of a deleted repository must be dropped")
		})
	}
}

// TestReconcileKindRetriesUnconfirmedStartOfRunningPipelineRun asserts that a
// failed state patch on an already running PipelineRun is retried. The patch
// only carries metadata for such a run, so a readback showing it is not
// pending proves nothing: without the state and reporting annotations the
// write did not land and the reconcile has to be retried.
func TestReconcileKindRetriesUnconfirmedStartOfRunningPipelineRun(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
			Annotations: map[string]string{
				keys.State:         kubeinteraction.StateQueued,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "true",
				keys.GitProvider:   "github",
			},
		},
		// Tekton already started it, so it is no longer pending before the
		// patch and the patch carries no spec change.
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
	}
	limit := 1
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
		Spec:       v1alpha1.RepositorySpec{ConcurrencyLimit: &limit},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pr},
		Repositories: []*v1alpha1.Repository{repo},
	})
	qm := queuepkg.NewManager(logger)
	_, err := qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(pr)})
	assert.NilError(t, err)

	stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewConflict(tektonv1.Resource("pipelineruns"), "test-pr", fmt.Errorf("stale start"))
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
				Pac:        &info.PacOpts{Settings: settings.Settings{}},
				Kube:       &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{Name: "default", GlobalRepository: "default-global"},
			},
		},
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
	}

	err = r.ReconcileKind(ctx, pr)
	assert.Assert(t, stderrors.Is(err, ErrPipelineRunNotStarted), "got %v", err)
	updated, getErr := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
	assert.NilError(t, getErr)
	assert.Equal(t, updated.GetAnnotations()[keys.State], kubeinteraction.StateQueued)
	_, reported := updated.GetAnnotations()[keys.SCMReportingPLRStarted]
	assert.Assert(t, !reported, "the reporting marker was never written")
	assert.DeepEqual(t, qm.RunningPipelineRuns(repo), []string{queuepkg.PrKey(pr)})
}

// TestReconcileKindDropsUnstartableRunningPipelineRun covers a PipelineRun that
// Tekton still reports as Running while its spec has already been cancelled or
// gracefully stopped, and whose start was never reported to the provider.
//
// Such a run can never be started, so the promotion path deliberately treats
// errPipelineRunGone as "nothing to do" and returns nil. Returning it here
// instead would requeue forever over a condition no retry can change. Graceful
// stop makes that window wide: Tekton lets running tasks drain, so the Running
// condition legitimately survives for as long as those tasks take.
func TestReconcileKindDropsUnstartableRunningPipelineRun(t *testing.T) {
	tests := []struct {
		name   string
		status tektonv1.PipelineRunSpecStatus
	}{
		{name: "cancelled", status: tektonv1.PipelineRunSpecStatusCancelled},
		{name: "cancelled run finally", status: tektonv1.PipelineRunSpecStatusCancelledRunFinally},
		{name: "stopped run finally", status: tektonv1.PipelineRunSpecStatusStoppedRunFinally},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pr", Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State:         kubeinteraction.StateStarted,
						keys.Repository:    "test-repo",
						keys.SecretCreated: "true",
						keys.GitProvider:   "github",
					},
				},
				Spec: tektonv1.PipelineRunSpec{Status: tt.status},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{{
							Type:   knativeapi.ConditionSucceeded,
							Status: corev1.ConditionUnknown,
							Reason: string(tektonv1.PipelineRunReasonRunning),
						}},
					},
				},
			}
			limit := 1
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
				Spec:       v1alpha1.RepositorySpec{ConcurrencyLimit: &limit},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				Repositories: []*v1alpha1.Repository{repo},
			})
			qm := queuepkg.NewManager(logger)
			_, err := qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(pr)})
			assert.NilError(t, err)

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         qm,
				run: &params.Run{
					Clients: clients.Clients{Tekton: stdata.Pipeline, Kube: stdata.Kube, Log: logger},
					Info: info.Info{
						Pac:        &info.PacOpts{Settings: settings.Settings{}},
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{Name: "default", GlobalRepository: "default-global"},
					},
				},
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			assert.NilError(t, r.ReconcileKind(ctx, pr),
				"an unstartable run must not be retried forever")
		})
	}
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
				qm:         queuepkg.NewManager(logger),
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

func TestReconcileKindEarlyBranches(t *testing.T) {
	tests := []struct {
		name            string
		seedPipelineRun bool
		seedRepository  bool
		annotations     map[string]string
		resourceVersion string
		wantErrSub      string
	}{
		{
			name: "missing pipelinerun returns get error",
			annotations: map[string]string{
				keys.State:      kubeinteraction.StateStarted,
				keys.Repository: "test-repo",
			},
			wantErrSub: "cannot get pipelineRun",
		},
		{
			name:            "stale resource version is ignored",
			seedPipelineRun: true,
			seedRepository:  true,
			resourceVersion: "stale",
			annotations: map[string]string{
				keys.State:      kubeinteraction.StateStarted,
				keys.Repository: "test-repo",
			},
		},
		{
			name:            "missing repository returns lister error",
			seedPipelineRun: true,
			annotations: map[string]string{
				keys.State:      kubeinteraction.StateStarted,
				keys.Repository: "test-repo",
			},
			wantErrSub: "failed to get repository CR",
		},
		{
			name:            "github app waits for checkrun id",
			seedPipelineRun: true,
			seedRepository:  true,
			annotations: map[string]string{
				keys.State:          kubeinteraction.StateStarted,
				keys.Repository:     "test-repo",
				keys.InstallationID: "1234",
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
					Name:            "test-pr",
					Namespace:       "test-ns",
					Annotations:     tt.annotations,
					ResourceVersion: tt.resourceVersion,
				},
			}
			seeded := pr.DeepCopy()
			seeded.ResourceVersion = ""

			testData := testclient.Data{}
			if tt.seedPipelineRun {
				testData.PipelineRuns = []*tektonv1.PipelineRun{seeded}
			}
			if tt.seedRepository {
				testData.Repositories = []*v1alpha1.Repository{{
					ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
				}}
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
						Log:    logger,
					},
					Info: info.Info{
						Pac:        info.NewPacOpts(),
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{Name: "default"},
					},
				},
			}

			err := r.ReconcileKind(ctx, pr)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestReconcileKindSecretCreationBranches(t *testing.T) {
	tests := []struct {
		name              string
		annotations       map[string]string
		getSecretResult   map[string]string
		createSecretError error
		wantErrSub        string
		wantLogSub        string
	}{
		{
			name: "basic auth secret creation error is returned",
			annotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			getSecretResult: map[string]string{
				"provider-secret": "test-token",
			},
			createSecretError: fmt.Errorf("connection timeout"),
			wantErrSub:        "creating basic auth secret",
		},
		{
			name:        "non basic auth secret error is logged and ignored",
			annotations: map[string]string{},
			wantLogSub:  "failed to create secret for pipelineRun test-ns/test-pr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.ErrorLevel)
			logger := zap.New(observer).Sugar()
			ctx = logging.WithLogger(ctx, logger)

			annotations := map[string]string{
				keys.State:         kubeinteraction.StateStarted,
				keys.Repository:    "test-repo",
				keys.SecretCreated: "false",
				keys.GitProvider:   "github",
				keys.RepoURL:       "https://github.com/org/repo",
				keys.URLOrg:        "org",
				keys.URLRepository: "repo",
				keys.SHA:           "abc123",
			}
			maps.Copy(annotations, tt.annotations)
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pr",
					Namespace:   "test-ns",
					Annotations: annotations,
				},
			}
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
				Spec: v1alpha1.RepositorySpec{
					GitProvider: &v1alpha1.GitProvider{
						Secret: &v1alpha1.Secret{Name: "provider-secret"},
					},
				},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				Repositories: []*v1alpha1.Repository{repo},
				ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
			})

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
							Settings: settings.Settings{SecretAutoCreation: true},
						},
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{GlobalRepository: "global-repo"},
					},
				},
				kinteract: &testkubernetestint.KinterfaceTest{
					GetSecretResult:   tt.getSecretResult,
					CreateSecretError: tt.createSecretError,
				},
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			err := r.ReconcileKind(ctx, pr)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			if tt.wantLogSub != "" {
				assert.Equal(t, log.FilterMessageSnippet(tt.wantLogSub).Len(), 1)
			}
		})
	}
}

func TestReconcileKindDonePathWithDetectedGitHubApp(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	_, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
	defer teardown()
	t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", ghTestServerURL+"/api/v3")

	const (
		checkRunID = "6566930541"
		secretName = "pipelines-as-code-secret"
	)
	mux.HandleFunc("/app/installations/1234/access_tokens", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"token":"ghs_test_token","expires_at":"2099-01-01T00:00:00Z"}`)
	})
	checkRunUpdates := 0
	mux.HandleFunc("/repos/random/app/check-runs/"+checkRunID, func(rw http.ResponseWriter, req *http.Request) {
		assert.Equal(t, req.Method, http.MethodPatch)
		checkRunUpdates++
		var updated github.UpdateCheckRunOptions
		assert.NilError(t, json.NewDecoder(req.Body).Decode(&updated))
		assert.Equal(t, updated.GetStatus(), "completed")
		assert.Equal(t, updated.GetConclusion(), finalSuccessStatus)
		fmt.Fprintf(rw, `{"id":%s}`, checkRunID)
	})

	clock := clockwork.NewFakeClock()
	pr := tektontest.MakePRCompletion(clock, "pipeline-newest", "ns", tektonv1.PipelineRunReasonSuccessful.String(), nil, map[string]string{}, 10)
	pr.Annotations = map[string]string{
		keys.GitAuthSecret:  "unused-for-github-app",
		keys.State:          kubeinteraction.StateStarted,
		keys.InstallationID: "1234",
		keys.RepoURL:        randomURL,
		keys.Repository:     pr.GetName(),
		keys.OriginalPRName: pr.GetName(),
		keys.GitProvider:    "github",
		keys.CheckRunID:     checkRunID,
		keys.URLOrg:         "random",
		keys.URLRepository:  "app",
		keys.SHA:            "123afc",
		keys.Branch:         "main",
		keys.SourceBranch:   "feature",
		keys.EventType:      "push",
	}
	pr.Labels = map[string]string{
		keys.Repository: pr.GetName(),
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: pr.GetName(), Namespace: pr.GetNamespace()},
		Spec: v1alpha1.RepositorySpec{
			URL: randomURL,
			GitProvider: &v1alpha1.GitProvider{
				URL: "https://github.com",
			},
		},
	}
	appSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: system.Namespace()},
		Data: map[string][]byte{
			keys.GithubApplicationID: []byte("12345"),
			keys.GithubPrivateKey:    []byte(reconcilerFakePrivateKey),
		},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pr},
		Repositories: []*v1alpha1.Repository{repo},
		Secret:       []*corev1.Secret{appSecret},
		ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
	})
	metrics, err := prmetrics.NewRecorder()
	assert.NilError(t, err)

	r := &Reconciler{
		repoLister: informers.Repository.Lister(),
		qm:         queuepkg.NewManager(logger),
		run: &params.Run{
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Tekton:         stdata.Pipeline,
				Kube:           stdata.Kube,
				Log:            logger,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{},
				},
				Kube: &info.KubeOpts{Namespace: system.Namespace()},
				Controller: &info.ControllerInfo{
					Name:             "default",
					Secret:           secretName,
					GlobalRepository: "global-repo",
				},
			},
		},
		pipelineRunLister: stdata.PipelineLister,
		kinteract: &testkubernetestint.KinterfaceTest{
			GetSecretResult: map[string]string{
				secretName: "webhook-secret",
			},
		},
		metrics:      metrics,
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
	}
	r.run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

	err = r.ReconcileKind(ctx, pr)
	assert.NilError(t, err)
	assert.Equal(t, checkRunUpdates, 1)
	updatedPR, err := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, updatedPR.Annotations[keys.State], kubeinteraction.StateCompleted)
}

func TestReconcileKindDonePathProviderErrors(t *testing.T) {
	tests := []struct {
		name             string
		annotations      map[string]string
		setup            func(t *testing.T, stdata testclient.Clients)
		wantErrSub       string
		wantLogSub       string
		wantEventReason  string
		setupGitHubMocks bool
	}{
		{
			name: "detect provider error is emitted and ignored",
			annotations: map[string]string{
				keys.GitProvider: "unknown",
			},
			wantLogSub: "detectProvider:",
		},
		{
			name: "report final status error is emitted and returned",
			annotations: map[string]string{
				keys.GitProvider: "github",
			},
			setup: func(t *testing.T, stdata testclient.Clients) {
				t.Helper()
				stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("patch failed")
				})
			},
			wantErrSub:       "cannot update state",
			wantLogSub:       "report status:",
			wantEventReason:  "RepositoryReportFinalStatus",
			setupGitHubMocks: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.ErrorLevel)
			logger := zap.New(observer).Sugar()
			ctx = logging.WithLogger(ctx, logger)

			var mux *http.ServeMux
			var ghTestServerURL string
			var teardown func()
			if tt.setupGitHubMocks {
				_, mux, ghTestServerURL, teardown = ghtesthelper.SetupGH()
				defer teardown()
				mux.HandleFunc("/repos/random/app/statuses/123afc", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"state":"success"}`)
				})
			}

			clock := clockwork.NewFakeClock()
			pr := tektontest.MakePRCompletion(clock, "pipeline-newest", "ns", tektonv1.PipelineRunReasonSuccessful.String(), nil, map[string]string{}, 10)
			pr.Annotations = map[string]string{
				keys.State:          kubeinteraction.StateStarted,
				keys.RepoURL:        randomURL,
				keys.Repository:     pr.GetName(),
				keys.OriginalPRName: pr.GetName(),
				keys.URLOrg:         "random",
				keys.URLRepository:  "app",
				keys.SHA:            "123afc",
				keys.EventType:      "push",
			}
			maps.Copy(pr.Annotations, tt.annotations)
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: pr.GetName(), Namespace: pr.GetNamespace()},
				Spec: v1alpha1.RepositorySpec{
					URL: randomURL,
					GitProvider: &v1alpha1.GitProvider{
						URL:    ghTestServerURL,
						Secret: &v1alpha1.Secret{Name: "provider-secret"},
					},
				},
			}
			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				Repositories: []*v1alpha1.Repository{repo},
				ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
			}
			if ghTestServerURL != "" {
				testData.ConfigMap = []*corev1.ConfigMap{{
					ObjectMeta: metav1.ObjectMeta{Name: "pipelines-as-code", Namespace: system.Namespace()},
					Data: map[string]string{
						settings.TrustedProviderHostnamesKey: strings.TrimPrefix(ghTestServerURL, "http://"),
					},
				}}
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			if tt.setup != nil {
				tt.setup(t, stdata)
			}
			metrics, err := prmetrics.NewRecorder()
			assert.NilError(t, err)

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         queuepkg.NewManager(logger),
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            logger,
					},
					Info: info.Info{
						Pac: &info.PacOpts{
							Settings: settings.Settings{},
						},
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{Name: "default", GlobalRepository: "global-repo"},
					},
				},
				kinteract: &testkubernetestint.KinterfaceTest{
					GetSecretResult: map[string]string{
						"provider-secret": "test-token",
					},
				},
				metrics:      metrics,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}
			r.run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			err = r.ReconcileKind(ctx, pr)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
			}
			assert.Assert(t, log.FilterMessageSnippet(tt.wantLogSub).Len() > 0, "expected log %q", tt.wantLogSub)
			if tt.wantEventReason != "" {
				events, listErr := stdata.Kube.CoreV1().Events(pr.Namespace).List(ctx, metav1.ListOptions{})
				assert.NilError(t, listErr)
				found := false
				for _, event := range events.Items {
					if event.Reason == tt.wantEventReason {
						found = true
					}
				}
				assert.Assert(t, found, "expected event reason %q", tt.wantEventReason)
			}
		})
	}
}

func TestReportFinalStatusErrorBranches(t *testing.T) {
	tests := []struct {
		name            string
		repository      *v1alpha1.Repository
		globalRepo      *v1alpha1.Repository
		seedPipelineRun bool
		kint            *testkubernetestint.KinterfaceTest
		provider        *reportFinalStatusProvider
		metrics         *prmetrics.Recorder
		pacInfo         *info.PacOpts
		setup           func(t *testing.T, stdata testclient.Clients)
		wantErrSub      string
		wantLogSub      string
		wantRunLogSet   bool
	}{
		{
			name:       "missing repository returns error",
			wantErrSub: "reportFinalStatus",
		},
		{
			name: "inherited global secret with local provider url returns error",
			repository: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
				Spec: v1alpha1.RepositorySpec{
					URL:         "https://github.com/org/repo",
					GitProvider: &v1alpha1.GitProvider{URL: "https://evil.example.com"},
				},
			},
			globalRepo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "global-repo", Namespace: "global"},
				Spec: v1alpha1.RepositorySpec{
					GitProvider: &v1alpha1.GitProvider{
						URL:    "https://github.com",
						Secret: &v1alpha1.Secret{Name: "global-secret"},
					},
				},
			},
			wantErrSub: "must not be sent to an endpoint chosen by another namespace",
		},
		{
			name:            "missing provider secret returns error",
			repository:      reportFinalStatusTestRepository(nil),
			seedPipelineRun: true,
			kint:            &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{}},
			wantErrSub:      "cannot get secret from repository",
		},
		{
			name:            "set client error returns error",
			repository:      reportFinalStatusTestRepository(nil),
			seedPipelineRun: true,
			kint: &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{
				"provider-secret": "test-token",
			}},
			provider:   &reportFinalStatusProvider{setClientErr: fmt.Errorf("client refused")},
			wantErrSub: "cannot set client",
		},
		{
			name:       "missing final pipelinerun warns and update state returns error",
			repository: reportFinalStatusTestRepository(nil),
			kint: &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{
				"provider-secret": "test-token",
			}},
			wantErrSub: "cannot update state",
		},
		{
			name:            "update state error returns error",
			repository:      reportFinalStatusTestRepository(nil),
			seedPipelineRun: true,
			kint: &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{
				"provider-secret": "test-token",
			}},
			setup: func(t *testing.T, stdata testclient.Clients) {
				t.Helper()
				stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("patch failed")
				})
			},
			wantErrSub: "cannot update state",
		},
		{
			name:            "cleanup error returns error",
			repository:      reportFinalStatusTestRepository(nil),
			seedPipelineRun: true,
			kint: &testkubernetestint.KinterfaceTest{
				ExpectedNumberofCleanups: 2,
				GetSecretResult: map[string]string{
					"provider-secret": "test-token",
				},
			},
			pacInfo: &info.PacOpts{
				Settings: settings.Settings{DefaultMaxKeepRuns: 1},
			},
			wantErrSub: "error cleaning pipelineruns",
		},
		{
			name:            "nil run logger is populated",
			repository:      reportFinalStatusTestRepository(nil),
			seedPipelineRun: true,
			kint: &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{
				"provider-secret": "test-token",
			}},
			wantRunLogSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.WarnLevel)
			logger := zap.New(observer).Sugar()

			pr := reportFinalStatusTestPipelineRun()
			testData := testclient.Data{}
			if tt.seedPipelineRun {
				testData.PipelineRuns = []*tektonv1.PipelineRun{pr}
			}
			if tt.repository != nil {
				testData.Repositories = append(testData.Repositories, tt.repository)
			}
			if tt.globalRepo != nil {
				testData.Repositories = append(testData.Repositories, tt.globalRepo)
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			if tt.setup != nil {
				tt.setup(t, stdata)
			}
			pacInfo := tt.pacInfo
			if pacInfo == nil {
				pacInfo = &info.PacOpts{Settings: settings.Settings{}}
			}
			metrics := tt.metrics
			if metrics == nil {
				var err error
				metrics, err = prmetrics.NewRecorder()
				assert.NilError(t, err)
			}
			kint := tt.kint
			if kint == nil {
				kint = &testkubernetestint.KinterfaceTest{}
			}
			vcx := tt.provider
			if vcx == nil {
				vcx = &reportFinalStatusProvider{}
			}

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         queuepkg.NewManager(logger),
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
					},
					Info: info.Info{
						Pac:        pacInfo,
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{GlobalRepository: "global-repo"},
					},
				},
				kinteract:    kint,
				metrics:      metrics,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}
			r.run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			_, err := r.reportFinalStatus(ctx, logger, pacInfo, buildEventFromPipelineRun(pr), pr, vcx)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
			}
			if tt.wantLogSub != "" {
				assert.Assert(t, log.FilterMessageSnippet(tt.wantLogSub).Len() > 0, "expected log %q", tt.wantLogSub)
			}
			if tt.wantRunLogSet {
				assert.Assert(t, r.run.Clients.Log != nil, "reportFinalStatus should populate a nil run logger")
			}
		})
	}
}

func TestReportFinalStatusNonBlockingBranches(t *testing.T) {
	tests := []struct {
		name            string
		repository      *v1alpha1.Repository
		provider        *reportFinalStatusProvider
		metrics         *prmetrics.Recorder
		setupBackoff    bool
		wantFinalState  string
		wantLogSub      string
		wantEventReason string
	}{
		{
			name:           "post final status error marks pipelinerun failed and continues",
			repository:     reportFinalStatusTestRepository(nil),
			provider:       &reportFinalStatusProvider{TestProviderImp: tprovider.TestProviderImp{CreateStatusErorring: true}},
			setupBackoff:   true,
			wantFinalState: kubeinteraction.StateFailed,
			wantLogSub:     "failed to post final status",
		},
		{
			name:           "metrics error is logged and ignored",
			repository:     reportFinalStatusTestRepository(nil),
			metrics:        &prmetrics.Recorder{},
			wantFinalState: kubeinteraction.StateCompleted,
			wantLogSub:     "failed to emit metrics",
		},
		{
			name: "llm analysis error emits warning and continues",
			repository: reportFinalStatusTestRepository(&v1alpha1.Settings{
				AIAnalysis: &v1alpha1.AIAnalysisConfig{Enabled: true},
			}),
			wantFinalState:  kubeinteraction.StateCompleted,
			wantLogSub:      "LLM analysis failed",
			wantEventReason: "LLMAnalysisFailed",
		},
		{
			name: "custom params error emits event and continues",
			repository: func() *v1alpha1.Repository {
				repo := reportFinalStatusTestRepository(nil)
				params := []v1alpha1.Params{{
					Name:   "broken",
					Value:  "value",
					Filter: "event ==",
				}}
				repo.Spec.Params = &params
				return repo
			}(),
			wantFinalState: kubeinteraction.StateCompleted,
			wantLogSub:     "error processing repository CR custom params",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupBackoff {
				oldBackoffSchedule := backoffSchedule
				backoffSchedule = []time.Duration{time.Millisecond}
				defer func() { backoffSchedule = oldBackoffSchedule }()
			}

			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.WarnLevel)
			logger := zap.New(observer).Sugar()
			pr := reportFinalStatusTestPipelineRun()
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				Repositories: []*v1alpha1.Repository{tt.repository},
			})
			metrics := tt.metrics
			if metrics == nil {
				var err error
				metrics, err = prmetrics.NewRecorder()
				assert.NilError(t, err)
			}
			vcx := tt.provider
			if vcx == nil {
				vcx = &reportFinalStatusProvider{}
			}

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         queuepkg.NewManager(logger),
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            logger,
					},
					Info: info.Info{
						Pac:        &info.PacOpts{Settings: settings.Settings{}},
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{GlobalRepository: "global-repo"},
					},
				},
				kinteract: &testkubernetestint.KinterfaceTest{
					GetSecretResult: map[string]string{
						"provider-secret": "test-token",
					},
				},
				metrics:      metrics,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}
			r.run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			_, err := r.reportFinalStatus(ctx, logger, r.run.Info.Pac, buildEventFromPipelineRun(pr), pr, vcx)
			assert.NilError(t, err)
			updatedPR, err := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, updatedPR.Annotations[keys.State], tt.wantFinalState)
			assert.Assert(t, log.FilterMessageSnippet(tt.wantLogSub).Len() > 0, "expected log %q", tt.wantLogSub)
			if tt.wantEventReason != "" {
				events, listErr := stdata.Kube.CoreV1().Events(pr.Namespace).List(ctx, metav1.ListOptions{})
				assert.NilError(t, listErr)
				found := false
				for _, event := range events.Items {
					if event.Reason == tt.wantEventReason {
						found = true
					}
				}
				assert.Assert(t, found, "expected event reason %q", tt.wantEventReason)
			}
		})
	}
}

func reportFinalStatusTestPipelineRun() *tektonv1.PipelineRun {
	clock := clockwork.NewFakeClock()
	pr := tektontest.MakePRCompletion(clock, "test-pr", "test-ns", tektonv1.PipelineRunReasonSuccessful.String(), nil, map[string]string{}, 10)
	pr.Annotations = map[string]string{
		keys.State:          kubeinteraction.StateStarted,
		keys.Repository:     "test-repo",
		keys.GitProvider:    "github",
		keys.RepoURL:        "https://github.com/org/repo",
		keys.URLOrg:         "org",
		keys.URLRepository:  "repo",
		keys.SHA:            "abc123",
		keys.EventType:      "push",
		keys.OriginalPRName: "test-pr",
	}
	return pr
}

func reportFinalStatusTestRepository(repoSettings *v1alpha1.Settings) *v1alpha1.Repository {
	return &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
		Spec: v1alpha1.RepositorySpec{
			URL:      "https://github.com/org/repo",
			Settings: repoSettings,
			GitProvider: &v1alpha1.GitProvider{
				URL:    "https://github.com",
				Secret: &v1alpha1.Secret{Name: "provider-secret"},
			},
		},
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
		qm: queuepkg.NewManager(logger),
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
