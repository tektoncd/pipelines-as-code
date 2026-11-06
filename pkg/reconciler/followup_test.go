package reconciler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testkubernetestint "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	fakepipelineclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"knative.dev/pkg/apis"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// finishedGroupRun builds a done run of follow-up group "group1", optionally with the
// given follow-ups stored on it.
func finishedGroupRun(t *testing.T, name, definition string, followUps ...prgroup.FollowUp) *tektonv1.PipelineRun {
	t.Helper()
	pr := groupRun(t, name, definition, "group1", followUps...)
	pr.Annotations[keys.State] = kubeinteraction.StateCompleted
	pr.Labels[keys.State] = kubeinteraction.StateCompleted
	pr.Status.Conditions = []apis.Condition{{
		Type:   apis.ConditionSucceeded,
		Status: corev1.ConditionTrue,
		Reason: "Succeeded",
	}}
	return pr
}

// storedFollowUpState reads the state of a follow-up stored on a run of the
// fake cluster, empty when the run or the follow-up is not there.
func storedFollowUpState(ctx context.Context, t *testing.T, tekton *fakepipelineclientset.Clientset,
	runName, id string,
) string {
	t.Helper()
	pr, err := tekton.TektonV1().PipelineRuns("namespace").Get(ctx, runName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	followUps, err := prgroup.FollowUpsOf(pr)
	assert.NilError(t, err)
	for _, followUp := range followUps {
		if followUp.ID == id {
			return followUp.State
		}
	}
	return ""
}

func TestConsumeFollowUp(t *testing.T) {
	deploy := prgroup.FollowUp{ID: "deploy-group1", Name: "deploy", Deps: []string{"build"}, State: prgroup.StatePending}
	tests := []struct {
		name      string
		seed      bool
		wantState string
	}{
		{
			name:      "a pending follow-up is moved to the terminal state",
			seed:      true,
			wantState: prgroup.StateSkipped,
		},
		{
			name: "a run that is gone stores nothing anymore",
			seed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			run := groupRun(t, "build", "build", "group1", deploy)
			data := testclient.Data{}
			if tt.seed {
				data.PipelineRuns = []*tektonv1.PipelineRun{run}
			}
			stdata, _ := testclient.SeedTestData(t, ctx, data)
			r := &Reconciler{run: &params.Run{Clients: clients.Clients{Tekton: stdata.Pipeline}}}

			fu := prgroup.FollowUpOnRun{FollowUp: deploy, Run: run}
			assert.NilError(t, r.consumeFollowUp(ctx, fu, prgroup.StateSkipped))
			assert.Equal(t, tt.wantState, storedFollowUpState(ctx, t, stdata.Pipeline, "build", deploy.ID))
		})
	}
}

func TestEvaluateFollowUpsOf(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T) (seed []*tektonv1.PipelineRun, dep *tektonv1.PipelineRun)
		wantState string
		// wantNotPending checks the follow-up was evaluated rather than its
		// exact outcome
		wantNotPending bool
	}{
		{
			// /cancel tests on build -> tests -> deploy: the controller
			// consumed tests as cancelled; the patch re-enqueues build
			name: "the dependent of a follow-up cancelled while waiting is evaluated",
			setup: func(t *testing.T) ([]*tektonv1.PipelineRun, *tektonv1.PipelineRun) {
				tests := prgroup.FollowUp{
					ID: "tests-group1", Name: "tests",
					Deps: []string{"build"}, State: prgroup.StateCancelled,
				}
				deploy := prgroup.FollowUp{
					ID: "deploy-group1", Name: "deploy",
					Deps: []string{"tests"}, State: prgroup.StatePending,
				}
				build := finishedGroupRun(t, "build", "build", tests, deploy)
				return []*tektonv1.PipelineRun{build}, build
			},
			wantNotPending: true,
		},
		{
			name: "a running dependency keeps the follow-up waiting",
			setup: func(t *testing.T) ([]*tektonv1.PipelineRun, *tektonv1.PipelineRun) {
				deploy := prgroup.FollowUp{
					ID: "deploy-group1", Name: "deploy",
					Deps: []string{"build", "tests"}, State: prgroup.StatePending,
				}
				build := groupRun(t, "build", "build", "group1", deploy)
				testsRun := finishedGroupRun(t, "tests", "tests")
				return []*tektonv1.PipelineRun{build, testsRun}, testsRun
			},
			wantState: prgroup.StatePending,
		},
		{
			name: "an existing run of the follow-up's name consumes it as created",
			setup: func(t *testing.T) ([]*tektonv1.PipelineRun, *tektonv1.PipelineRun) {
				deploy := prgroup.FollowUp{
					ID: "deploy-group1", Name: "deploy",
					Deps: []string{"build"}, State: prgroup.StatePending,
				}
				build := finishedGroupRun(t, "build", "build", deploy)
				created := groupRun(t, "deploy-group1", "deploy", "group1")
				return []*tektonv1.PipelineRun{build, created}, build
			},
			wantState: prgroup.StateCreated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			seed, dep := tt.setup(t)
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: seed})
			logger := zap.NewNop().Sugar()
			r := &Reconciler{
				run:          &params.Run{Clients: clients.Clients{Tekton: stdata.Pipeline, Kube: stdata.Kube}},
				repoLister:   informers.Repository.Lister(),
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			assert.NilError(t, r.evaluateFollowUpsOf(ctx, logger, dep))
			state := storedFollowUpState(ctx, t, stdata.Pipeline, "build", "deploy-group1")
			if tt.wantNotPending {
				assert.Assert(t, state != prgroup.StatePending, "the follow-up was not evaluated")
				return
			}
			assert.Equal(t, tt.wantState, state)
		})
	}
}

func TestSkipFollowUpQuietly(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	deploy := prgroup.FollowUp{ID: "deploy-group1", Name: "deploy", Deps: []string{"build"}, State: prgroup.StatePending}
	build := finishedGroupRun(t, "build", "build", deploy)
	build.Annotations[keys.Repository] = "repo"
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{
			{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"}},
		},
		PipelineRuns: []*tektonv1.PipelineRun{build},
	})
	logger := zap.NewNop().Sugar()
	r := &Reconciler{
		repoLister:   informers.Repository.Lister(),
		eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
		run:          &params.Run{Clients: clients.Clients{Tekton: stdata.Pipeline, Kube: stdata.Kube}},
	}

	// a false CEL condition is the pipeline author's: no check, just the
	// terminal state and the re-evaluation of the dependents
	fu := prgroup.FollowUpOnRun{FollowUp: deploy, Run: build}
	assert.NilError(t, r.skipFollowUpQuietly(ctx, logger, fu, build, "CEL condition evaluated to false"))
	assert.Equal(t, prgroup.StateSkipped, storedFollowUpState(ctx, t, stdata.Pipeline, "build", deploy.ID))
}

func TestMigrateDependentFollowUps(t *testing.T) {
	tests := []struct {
		name        string
		publishDeps []string
		seedCreated bool
		// conflicts is the number of patches of the created run answered
		// with a 409, like a Tekton status update racing the migration
		conflicts int
		wantOn    string
	}{
		{
			name:        "a pending dependent moves to the created run",
			publishDeps: []string{"deploy"},
			seedCreated: true,
			wantOn:      "deploy",
		},
		{
			name:        "a follow-up waiting on another definition stays put",
			publishDeps: []string{"build"},
			seedCreated: true,
			wantOn:      "tests",
		},
		{
			name:        "a conflict on the created run is retried",
			publishDeps: []string{"deploy"},
			seedCreated: true,
			conflicts:   1,
			wantOn:      "deploy",
		},
		{
			name:        "a created run that keeps conflicting leaves the follow-up where it is",
			publishDeps: []string{"deploy"},
			seedCreated: true,
			conflicts:   100,
			wantOn:      "tests",
		},
		{
			name:        "a created run that is gone leaves the follow-up where it is",
			publishDeps: []string{"deploy"},
			seedCreated: false,
			wantOn:      "tests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			publish := prgroup.FollowUp{
				ID: "publish-group1", Name: "publish",
				Deps: tt.publishDeps, State: prgroup.StatePending,
			}
			testsRun := groupRun(t, "tests", "tests", "group1", publish)
			created := groupRun(t, "deploy", "deploy", "group1")
			seed := []*tektonv1.PipelineRun{testsRun}
			if tt.seedCreated {
				seed = append(seed, created)
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: seed})
			conflicts := tt.conflicts
			stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				patch, ok := action.(k8stesting.PatchAction)
				if !ok || patch.GetName() != "deploy" || conflicts == 0 {
					return false, nil, nil
				}
				conflicts--
				return true, nil, k8serrors.NewConflict(tektonv1.Resource("pipelineruns"), "deploy", fmt.Errorf("modified"))
			})
			r := &Reconciler{run: &params.Run{Clients: clients.Clients{Tekton: stdata.Pipeline}}}

			collected := []prgroup.FollowUpOnRun{{FollowUp: publish, Run: testsRun}}
			r.migrateDependentFollowUps(ctx, zap.NewNop().Sugar(), created, collected)

			// the follow-up sits on exactly one run: the wanted one, still pending
			for _, runName := range []string{"tests", "deploy"} {
				state := storedFollowUpState(ctx, t, stdata.Pipeline, runName, publish.ID)
				if runName == tt.wantOn {
					assert.Equal(t, prgroup.StatePending, state)
				} else {
					assert.Equal(t, "", state)
				}
			}
		})
	}
}

func TestCreateFollowUp(t *testing.T) {
	tests := []struct {
		name             string
		concurrencyLimit int
		// conflictStart answers the start patch of the created run with a
		// 409, like a Tekton status update racing it
		conflictStart bool
		// rejectCreate makes the API server refuse the creation
		rejectCreate bool
		// pendOnCreate pends the run on creation, like an external
		// mutating webhook (a Kueue-like queue)
		pendOnCreate bool
		// lostRace makes the creation lose to a concurrent reconcile
		lostRace bool
		// cancelledMeanwhile cancels the stored follow-up while its run is
		// created, like a /cancel racing the watcher
		cancelledMeanwhile bool
		wantCancelled      bool
		wantSecretDeleted  bool
		wantQueued         bool
		wantState          string
		wantPending        bool
		wantFollowUp       string
		wantCheckFail      bool
	}{
		{
			name:              "a rejected creation fails the follow-up like a run that cannot start",
			rejectCreate:      true,
			wantFollowUp:      prgroup.StateFailed,
			wantCheckFail:     true,
			wantSecretDeleted: true,
		},
		{
			name:              "losing the creation race removes the git auth secret it created",
			lostRace:          true,
			wantFollowUp:      prgroup.StateCreated,
			wantSecretDeleted: true,
		},
		{
			name:          "a lost start patch leaves the run reconcilable and the follow-up consumed",
			conflictStart: true,
			wantState:     kubeinteraction.StateStarted,
			wantFollowUp:  prgroup.StateCreated,
		},
		{
			name:         "a follow-up pended by an external webhook is reported queued and left pending",
			pendOnCreate: true,
			wantState:    kubeinteraction.StateQueued,
			wantPending:  true,
			wantQueued:   true,
			wantFollowUp: prgroup.StateCreated,
		},
		{
			name:             "a follow-up under a concurrency limit is created queued",
			concurrencyLimit: 1,
			wantState:        kubeinteraction.StateQueued,
			wantPending:      true,
			wantQueued:       true,
			wantFollowUp:     prgroup.StateCreated,
		},
		{
			name:               "a follow-up cancelled while its run is created cancels the run and stays cancelled",
			cancelledMeanwhile: true,
			wantState:          kubeinteraction.StateStarted,
			wantFollowUp:       prgroup.StateCancelled,
			wantCancelled:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			logger := zap.NewNop().Sugar()
			deploy := prgroup.FollowUp{ID: "deploy-group1", Name: "deploy", Deps: []string{"build"}, State: prgroup.StatePending}
			publish := prgroup.FollowUp{ID: "publish-group1", Name: "publish", Deps: []string{"deploy"}, State: prgroup.StatePending}
			build := finishedGroupRun(t, "build", "build", deploy, publish)
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: []*tektonv1.PipelineRun{build}})
			if tt.rejectCreate {
				stdata.Pipeline.PrependReactor("create", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, k8serrors.NewServiceUnavailable("try again later")
				})
			}
			if tt.lostRace {
				stdata.Pipeline.PrependReactor("create", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, k8serrors.NewAlreadyExists(tektonv1.Resource("pipelineruns"), deploy.ID)
				})
			}
			if tt.cancelledMeanwhile {
				// the reactor runs under the fake client's lock: the
				// tracker is updated directly
				gvr := tektonv1.SchemeGroupVersion.WithResource("pipelineruns")
				stdata.Pipeline.PrependReactor("create", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					obj, err := stdata.Pipeline.Tracker().Get(gvr, "namespace", "build")
					if err != nil {
						return true, nil, err
					}
					stored, ok := obj.(*tektonv1.PipelineRun)
					if !ok {
						return true, nil, fmt.Errorf("unexpected object %T", obj)
					}
					host := stored.DeepCopy()
					if err := prgroup.ConsumeFollowUp(host, deploy.ID, prgroup.StateCancelled); err != nil {
						return true, nil, err
					}
					return false, nil, stdata.Pipeline.Tracker().Update(gvr, host, "namespace")
				})
			}
			if tt.pendOnCreate {
				stdata.Pipeline.PrependReactor("create", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
					if create, ok := action.(k8stesting.CreateAction); ok {
						if pr, ok := create.GetObject().(*tektonv1.PipelineRun); ok {
							pr.Spec.Status = tektonv1.PipelineRunSpecStatusPending
						}
					}
					return false, nil, nil
				})
			}
			if tt.conflictStart {
				stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
					patch, ok := action.(k8stesting.PatchAction)
					if !ok || patch.GetName() != deploy.ID {
						return false, nil, nil
					}
					return true, nil, k8serrors.NewConflict(tektonv1.Resource("pipelineruns"), deploy.ID, fmt.Errorf("modified"))
				})
			}
			cs := &params.Run{
				Clients: clients.Clients{Tekton: stdata.Pipeline, Kube: stdata.Kube, Log: logger},
				Info: info.Info{
					Pac:        info.NewPacOpts(),
					Kube:       &info.KubeOpts{Namespace: "global"},
					Controller: &info.ControllerInfo{Name: "default"},
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			kint := &testkubernetestint.KinterfaceTest{}
			r := &Reconciler{
				run:          cs,
				repoLister:   informers.Repository.Lister(),
				kinteract:    kint,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}
			vcx := &statusRecorder{}

			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"}}
			if tt.concurrencyLimit > 0 {
				repo.Spec.ConcurrencyLimit = &tt.concurrencyLimit
			}
			event := info.NewEvent()
			event.SHA = "abc123"
			run := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				Name:   "deploy",
				Labels: map[string]string{},
				Annotations: map[string]string{
					keys.OriginalPRName: "deploy",
					keys.GitAuthSecret:  "pac-gitauth-deploy",
				},
			}}
			fu := prgroup.FollowUpOnRun{FollowUp: deploy, Run: build}
			collected := []prgroup.FollowUpOnRun{fu, {FollowUp: publish, Run: build}}

			assert.NilError(t, r.createFollowUp(ctx, logger, fu, collected, build, repo,
				vcx, event, run, prgroup.Decision{}))
			assert.Equal(t, tt.wantFollowUp, storedFollowUpState(ctx, t, stdata.Pipeline, "build", deploy.ID))

			// nothing retries a failed creation: it is reported once
			failures := 0
			for _, status := range vcx.statuses {
				if status.Conclusion == providerstatus.ConclusionFailure {
					failures++
					assert.Equal(t, "pipelinerun start failure", status.Title)
					assert.Equal(t, deploy.ID, status.PipelineRunName)
					assert.Assert(t, strings.Contains(status.Text, "Tekton Controller has reported this error"), status.Text)
				}
			}
			assert.Equal(t, tt.wantCheckFail, failures == 1)
			queuedReported := slices.ContainsFunc(vcx.statuses, func(status providerstatus.StatusOpts) bool {
				return status.Status == "queued"
			})
			assert.Equal(t, tt.wantQueued, queuedReported)
			assert.Equal(t, tt.wantSecretDeleted, kint.SecretDeleted)
			if tt.rejectCreate || tt.lostRace {
				_, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, deploy.ID, metav1.GetOptions{})
				assert.Assert(t, k8serrors.IsNotFound(err))
				return
			}

			created, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, deploy.ID, metav1.GetOptions{})
			assert.NilError(t, err)
			// the watcher only reconciles runs carrying the state annotation
			assert.Equal(t, tt.wantState, created.GetAnnotations()[keys.State])
			assert.Equal(t, tt.wantPending, created.Spec.Status == tektonv1.PipelineRunSpecStatusPending)
			assert.Equal(t, tt.wantCancelled, created.IsGracefullyCancelled())
			if !tt.conflictStart && !tt.cancelledMeanwhile {
				// the chained follow-up moved onto its new direct dependency
				assert.Equal(t, prgroup.StatePending, storedFollowUpState(ctx, t, stdata.Pipeline, deploy.ID, publish.ID))
			}
		})
	}
}

// statusRecorder records the checks posted through the provider.
type statusRecorder struct {
	testprovider.TestProviderImp
	statuses []providerstatus.StatusOpts
}

func (v *statusRecorder) CreateStatus(_ context.Context, _ *info.Event, opts providerstatus.StatusOpts) error {
	v.statuses = append(v.statuses, opts)
	return nil
}
