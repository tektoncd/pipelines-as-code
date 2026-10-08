package reconciler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"strconv"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	fakepipeline "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	knativeapi "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

type queueRecoveryFixture struct {
	ctx         context.Context
	r           *Reconciler
	qm          *queuepkg.Manager
	cs          *fakepipeline.Clientset
	repo        *v1alpha1.Repository
	owner       *tektonv1.PipelineRun
	repoIndexer cache.Indexer
}

func newQueueRecoveryFixture(t *testing.T, limit int, initial bool) *queueRecoveryFixture {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	logger := zap.NewNop().Sugar()
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Spec:       v1alpha1.RepositorySpec{ConcurrencyLimit: &limit},
	}
	prs := make([]*tektonv1.PipelineRun, 0, 4)
	for _, name := range []string{"owner", "first", "second", "third"} {
		prs = append(prs, &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "ns", UID: types.UID(name), ResourceVersion: "1",
				Annotations: map[string]string{
					keys.Repository: "repo", keys.State: kubeinteraction.StateQueued,
					keys.ExecutionOrder: "ns/first,ns/second,ns/third", keys.SecretCreated: "true",
				},
			},
			Spec: tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusPending},
		})
	}
	owner := prs[0]
	owner.Spec.Status = ""
	owner.Annotations[keys.State] = kubeinteraction.StateStarted
	owner.Status.Status = knativeduckv1.Status{Conditions: knativeduckv1.Conditions{{
		Type: knativeapi.ConditionSucceeded, Status: corev1.ConditionTrue, Reason: string(tektonv1.PipelineRunReasonSuccessful),
	}}}
	if initial {
		owner = prs[1]
	}
	seed, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo}, PipelineRuns: prs,
	})
	qm := queuepkg.NewManager(logger)
	if !initial {
		_, err := qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(owner)})
		assert.NilError(t, err)
		assert.NilError(t, qm.AddToPendingQueue(repo, []string{"ns/first", "ns/second", "ns/third"}))
	}
	recorder, err := prmetrics.NewRecorder()
	assert.NilError(t, err)
	run := &params.Run{
		Clients: clients.Clients{Tekton: seed.Pipeline, Kube: seed.Kube, Log: logger},
		Info: info.Info{
			Pac: &info.PacOpts{}, Kube: &info.KubeOpts{Namespace: "global"},
			Controller: &info.ControllerInfo{GlobalRepository: "global"},
		},
	}
	run.Clients.SetConsoleUI(consoleui.FallBackConsole{})
	r := &Reconciler{
		qm: qm, run: run, repoLister: informers.Repository.Lister(),
		kinteract: &kubeinteraction.Interaction{Run: run}, metrics: recorder,
		eventEmitter: events.NewEventEmitter(seed.Kube, logger),
	}
	f := &queueRecoveryFixture{ctx: ctx, r: r, qm: qm, cs: seed.Pipeline, repo: repo, owner: owner, repoIndexer: informers.Repository.Informer().GetIndexer()}
	// The fake tracker does not enforce optimistic concurrency or advance RVs.
	seed.Pipeline.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return f.applyPatch(t, action)
	})
	return f
}

func (f *queueRecoveryFixture) get(t *testing.T, name string) *tektonv1.PipelineRun {
	t.Helper()
	pr, err := f.cs.TektonV1().PipelineRuns("ns").Get(f.ctx, name, metav1.GetOptions{})
	assert.NilError(t, err)
	return pr
}

func (f *queueRecoveryFixture) applyPatch(t *testing.T, action k8stesting.Action) (bool, runtime.Object, error) {
	t.Helper()
	patch, ok := action.(k8stesting.PatchAction)
	assert.Assert(t, ok)
	var body struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}
	assert.NilError(t, json.Unmarshal(patch.GetPatch(), &body))
	resource := tektonv1.SchemeGroupVersion.WithResource("pipelineruns")
	obj, err := f.cs.Tracker().Get(resource, "ns", patch.GetName())
	if err != nil {
		return true, nil, err
	}
	current, ok := obj.(*tektonv1.PipelineRun)
	assert.Assert(t, ok)
	if body.Metadata.Annotations[keys.State] == kubeinteraction.StateStarted {
		assert.Assert(t, body.Metadata.UID != "")
		assert.Assert(t, body.Metadata.ResourceVersion != "")
		if current.UID != body.Metadata.UID || current.ResourceVersion != body.Metadata.ResourceVersion {
			return true, nil, apierrors.NewConflict(tektonv1.Resource("pipelineruns"), current.Name, fmt.Errorf("stale start"))
		}
	}
	handled, result, err := k8stesting.ObjectReaction(f.cs.Tracker())(action)
	if err != nil {
		return handled, result, err
	}
	updated, ok := result.(*tektonv1.PipelineRun)
	assert.Assert(t, ok)
	rv, err := strconv.Atoi(current.ResourceVersion)
	assert.NilError(t, err)
	updated.ResourceVersion = strconv.Itoa(rv + 1)
	assert.NilError(t, f.cs.Tracker().Update(resource, updated, "ns"))
	return true, updated, nil
}

func (f *queueRecoveryFixture) call(mode string) error {
	switch mode {
	case "initial":
		return f.r.queuePipelineRun(f.ctx, f.r.run.Clients.Log, f.owner)
	case "finalization":
		return f.r.FinalizeKind(f.ctx, f.owner)
	case "abandonment":
		return f.r.ReconcileKind(f.ctx, f.owner)
	default:
		provider := &testprovider.TestProviderImp{}
		event := &info.Event{InstallationID: 1, Provider: &info.Provider{}}
		_, err := f.r.reportFinalStatus(f.ctx, f.r.run.Clients.Log, f.r.run.Info.Pac, event, f.owner, provider)
		return err
	}
}

func TestQueueRecoveryPatchOutcomes(t *testing.T) {
	tests := []struct {
		name           string
		apply          bool
		readFails      bool
		retryReadFails bool
	}{
		{name: "lost response after committed start", apply: true},
		{name: "pending readback keeps an uncertain start owned"},
		{name: "uncertain readback keeps an uncertain start owned", readFails: true},
		{name: "a later transient read cannot requeue an uncertain start", retryReadFails: true},
	}
	for _, mode := range []string{"initial", "finalization", "abandonment", "normal completion"} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				f := newQueueRecoveryFixture(t, 1, mode == "initial")
				failed := false
				f.cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
					patch, ok := action.(k8stesting.PatchAction)
					if !ok || patch.GetName() != "first" || failed {
						return false, nil, nil
					}
					failed = true
					if tt.apply {
						_, _, err := f.applyPatch(t, action)
						assert.NilError(t, err)
					}
					return true, nil, io.ErrUnexpectedEOF
				})
				readFailed := false
				f.cs.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
					if get, ok := action.(k8stesting.GetAction); ok && tt.readFails && failed && !readFailed && get.GetName() == "first" {
						readFailed = true
						return true, nil, apierrors.NewServiceUnavailable("uncertain readback")
					}
					return false, nil, nil
				})
				err := f.call(mode)
				switch {
				case !tt.apply:
					assert.Assert(t, stderrors.Is(err, ErrPipelineRunNotStarted), "got %v", err)
				case mode == "initial":
					assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "only post-start reporting may fail: %v", err)
				default:
					assert.NilError(t, err)
				}
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
				assert.Equal(t, f.get(t, "second").Spec.Status, tektonv1.PipelineRunSpecStatus(tektonv1.PipelineRunSpecStatusPending))
				if !tt.apply {
					assert.Equal(t, f.get(t, f.owner.Name).Annotations[keys.State], f.owner.Annotations[keys.State])
					if tt.retryReadFails {
						failRead := true
						f.cs.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
							if get, ok := action.(k8stesting.GetAction); ok && get.GetName() == "first" && failRead {
								failRead = false
								return true, nil, apierrors.NewServiceUnavailable("retry read")
							}
							return false, nil, nil
						})
						assert.ErrorContains(t, f.call(mode), "retry read")
						assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
					}
					err = f.call(mode)
					if mode == "initial" {
						assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "got %v", err)
					} else {
						assert.NilError(t, err)
					}
				}
				assert.Equal(t, string(f.get(t, "first").Spec.Status), "")
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
				// The actual reconciliation retry must terminate after reporting
				// fails for an already-started candidate.
				assert.NilError(t, f.r.ReconcileKind(f.ctx, f.get(t, f.owner.Name)))
				assert.Equal(t, f.get(t, "second").Annotations[keys.State], kubeinteraction.StateQueued)
			})
		}
	}
}

func TestQueueRecoveryTransientGet(t *testing.T) {
	for _, mode := range []string{"initial", "finalization", "abandonment", "normal completion"} {
		t.Run(mode, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 1, mode == "initial")
			gets := 0
			failAt := 1
			if mode == "initial" {
				failAt = 2 // The first read builds the execution-order list.
			}
			f.cs.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if get, ok := action.(k8stesting.GetAction); ok && get.GetName() == "first" {
					gets++
					if gets == failAt {
						return true, nil, apierrors.NewServiceUnavailable("transient successor read")
					}
				}
				return false, nil, nil
			})
			assert.ErrorContains(t, f.call(mode), "transient successor read")
			assert.Equal(t, len(f.qm.RunningPipelineRuns(f.repo)), 0)
			assert.Equal(t, len(f.qm.QueuedPipelineRuns(f.repo)), 3)
			assert.Equal(t, f.get(t, "first").Annotations[keys.State], kubeinteraction.StateQueued)
			assert.Equal(t, f.get(t, f.owner.Name).Annotations[keys.State], f.owner.Annotations[keys.State])
			err := f.call(mode)
			if mode == "initial" {
				assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "got %v", err)
			} else {
				assert.NilError(t, err)
			}
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
			assert.Equal(t, string(f.get(t, "first").Spec.Status), "")
			assert.Equal(t, f.get(t, "second").Annotations[keys.State], kubeinteraction.StateQueued)
		})
	}
}

func TestQueueRecoveryTerminalPatchReplay(t *testing.T) {
	tests := []struct {
		name  string
		apply bool
		limit int
	}{
		{name: "before applying at limit one", limit: 1},
		{name: "after applying at limit one", limit: 1, apply: true},
		{name: "before applying with spare capacity", limit: 3},
		{name: "after applying with spare capacity", limit: 3, apply: true},
	}
	for _, mode := range []string{"abandonment", "normal completion"} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				f := newQueueRecoveryFixture(t, tt.limit, false)
				failed := false
				f.cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
					patch, ok := action.(k8stesting.PatchAction)
					if !ok || patch.GetName() != "owner" || failed {
						return false, nil, nil
					}
					failed = true
					if tt.apply {
						_, _, err := f.applyPatch(t, action)
						assert.NilError(t, err)
					}
					return true, nil, io.ErrUnexpectedEOF
				})
				assert.ErrorContains(t, f.call(mode), "cannot update state")
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
				if tt.apply {
					assert.NilError(t, f.r.ReconcileKind(f.ctx, f.get(t, "owner")))
				} else {
					assert.NilError(t, f.call(mode))
				}
				// A repeated finalizer after the record has been cleaned up also
				// cannot manufacture a second handoff for an absent reservation.
				assert.NilError(t, f.r.FinalizeKind(f.ctx, f.owner))
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
				assert.Equal(t, f.get(t, "second").Annotations[keys.State], kubeinteraction.StateQueued)
			})
		}
	}
}

func TestQueueRecoveryDelayedPatch(t *testing.T) {
	tests := []struct {
		name          string
		change        func(*tektonv1.PipelineRun)
		settleOnRetry bool
	}{
		{name: "delayed request commits before the owner retries"},
		{name: "successful retry fences the original request", settleOnRetry: true},
		{name: "cancelled candidate", change: func(pr *tektonv1.PipelineRun) { pr.Spec.Status = tektonv1.PipelineRunSpecStatusCancelled }},
		{name: "gracefully cancelled candidate", change: func(pr *tektonv1.PipelineRun) { pr.Spec.Status = tektonv1.PipelineRunSpecStatusCancelledRunFinally }},
		{name: "gracefully stopped candidate", change: func(pr *tektonv1.PipelineRun) { pr.Spec.Status = tektonv1.PipelineRunSpecStatusStoppedRunFinally }},
		{name: "completed candidate", change: func(pr *tektonv1.PipelineRun) { pr.Annotations[keys.State] = kubeinteraction.StateCompleted }},
		{name: "deleted candidate", change: func(pr *tektonv1.PipelineRun) { now := metav1.Now(); pr.DeletionTimestamp = &now }},
		{name: "replacement UID", change: func(pr *tektonv1.PipelineRun) { pr.UID = "replacement" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 1, false)
			var delayed k8stesting.Action
			f.cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if patch, ok := action.(k8stesting.PatchAction); ok && patch.GetName() == "first" && delayed == nil {
					delayed = action.DeepCopy()
					return true, nil, io.ErrUnexpectedEOF
				}
				return false, nil, nil
			})
			assert.ErrorContains(t, f.call("finalization"), "start is not confirmed")
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
			switch {
			case tt.change != nil:
				pr := f.get(t, "first")
				tt.change(pr)
				pr.ResourceVersion = "2"
				assert.NilError(t, f.cs.Tracker().Update(tektonv1.SchemeGroupVersion.WithResource("pipelineruns"), pr, "ns"))
				assert.NilError(t, f.call("finalization"))
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/second"})
				_, _, err := f.applyPatch(t, delayed)
				assert.Assert(t, apierrors.IsConflict(err), "a delayed start must be fenced: %v", err)
				assert.Assert(t, f.get(t, "first").Spec.Status != "")
			case tt.settleOnRetry:
				assert.NilError(t, f.call("finalization"))
				_, _, err := f.applyPatch(t, delayed)
				assert.Assert(t, apierrors.IsConflict(err))
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
				assert.Equal(t, f.get(t, "second").Annotations[keys.State], kubeinteraction.StateQueued)
			default:
				_, _, err := f.applyPatch(t, delayed)
				assert.NilError(t, err)
				assert.NilError(t, f.call("finalization"))
				assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
				assert.Equal(t, f.get(t, "second").Annotations[keys.State], kubeinteraction.StateQueued)
			}
		})
	}
}

func TestQueueRecoveryBatchAndCancellation(t *testing.T) {
	tests := []struct {
		name      string
		cancel    bool
		failFirst bool
	}{
		{name: "started owner resumes another candidate from its batch"},
		{name: "first failure does not abandon the rest of the batch", failFirst: true},
		{name: "cancelled context preserves owned work", cancel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 2, true)
			failedName := "second"
			if tt.failFirst {
				failedName = "first"
			}
			failed := false
			f.cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if patch, ok := action.(k8stesting.PatchAction); ok && patch.GetName() == failedName && !failed {
					failed = true
					return true, nil, io.ErrUnexpectedEOF
				}
				return false, nil, nil
			})
			assert.ErrorContains(t, f.call("initial"), "start is not confirmed")
			assert.Equal(t, len(f.qm.RunningPipelineRuns(f.repo)), 2)
			startedName := "first"
			if tt.failFirst {
				startedName = "second"
			}
			assert.Equal(t, string(f.get(t, startedName).Spec.Status), "")
			if tt.cancel {
				ctx, cancel := context.WithCancel(f.ctx)
				cancel()
				assert.Assert(t, stderrors.Is(f.r.ReconcileKind(ctx, f.get(t, "first")), ctx.Err()))
				assert.Equal(t, len(f.qm.RunningPipelineRuns(f.repo)), 2)
			}

			err := f.r.ReconcileKind(f.ctx, f.get(t, "first"))
			assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "only reporting may fail after resuming: %v", err)
			assert.Equal(t, string(f.get(t, "second").Spec.Status), "")
			assert.Equal(t, f.get(t, "third").Annotations[keys.State], kubeinteraction.StateQueued)
			assert.NilError(t, f.r.ReconcileKind(f.ctx, f.get(t, "first")))
		})
	}
}

func TestQueueRecoveryCancelledBeforeStart(t *testing.T) {
	for _, mode := range []string{"initial", "finalization"} {
		t.Run(mode, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 1, mode == "initial")
			ctx := f.ctx
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			f.ctx = cancelled
			assert.Assert(t, stderrors.Is(f.call(mode), cancelled.Err()))
			assert.Equal(t, len(f.qm.RunningPipelineRuns(f.repo)), 0)
			f.ctx = ctx
			err := f.call(mode)
			if mode == "initial" {
				assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "got %v", err)
			} else {
				assert.NilError(t, err)
			}
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
		})
	}
}

func TestQueueRecoveryGoneAdmissionOwner(t *testing.T) {
	tests := []struct {
		name string
		gone bool
	}{
		{name: "deleted owner", gone: true},
		{name: "cancelled owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 1, true)
			failed := false
			f.cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if patch, ok := action.(k8stesting.PatchAction); ok && patch.GetName() == "first" && !failed {
					failed = true
					return true, nil, io.ErrUnexpectedEOF
				}
				return false, nil, nil
			})
			assert.ErrorContains(t, f.call("initial"), "start is not confirmed")
			pr := f.get(t, "first")
			resource := tektonv1.SchemeGroupVersion.WithResource("pipelineruns")
			if tt.gone {
				assert.NilError(t, f.cs.Tracker().Delete(resource, "ns", "first"))
			} else {
				pr.Spec.Status = tektonv1.PipelineRunSpecStatusCancelled
				pr.ResourceVersion = "2"
				assert.NilError(t, f.cs.Tracker().Update(resource, pr, "ns"))
			}
			err := f.r.FinalizeKind(f.ctx, pr)
			if tt.gone {
				// The former owner can still finish its initial admission.
				assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "got %v", err)
				assert.NilError(t, f.r.FinalizeKind(f.ctx, pr))
			} else {
				assert.NilError(t, err)
			}
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/second"})
			assert.Equal(t, string(f.get(t, "second").Spec.Status), "")
		})
	}
}

func TestQueueRecoveryExecutionOrderRead(t *testing.T) {
	f := newQueueRecoveryFixture(t, 1, true)
	failed := false
	f.cs.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if get, ok := action.(k8stesting.GetAction); ok && get.GetName() == "first" && !failed {
			failed = true
			return true, nil, apierrors.NewServiceUnavailable("execution-order read")
		}
		return false, nil, nil
	})
	assert.ErrorContains(t, f.call("initial"), "execution-order read")
	assert.Equal(t, len(f.qm.RunningPipelineRuns(f.repo)), 0)
	assert.Assert(t, stderrors.Is(f.call("initial"), ErrProviderNotConfigured))
	assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
}

func TestQueueRecoveryReconcileLimitReduction(t *testing.T) {
	tests := []struct {
		name      string
		inherited bool
	}{
		{name: "local concurrency limit"},
		{name: "inherited global concurrency limit", inherited: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 2, true)
			configRepo := f.repo.DeepCopy()
			if tt.inherited {
				f.repo = f.repo.DeepCopy()
				f.repo.Spec.ConcurrencyLimit = nil
				f.repo.Spec.GitProvider = &v1alpha1.GitProvider{}
				assert.NilError(t, f.repoIndexer.Update(f.repo))
				configRepo.Name = "global"
				configRepo.Namespace = "global"
				configRepo.Spec.GitProvider = &v1alpha1.GitProvider{
					Secret: &v1alpha1.Secret{Name: "global-secret"},
				}
				assert.NilError(t, f.repoIndexer.Add(configRepo))
			}
			gets := map[string]int{}
			f.cs.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				get, ok := action.(k8stesting.GetAction)
				assert.Assert(t, ok)
				name := get.GetName()
				gets[name]++
				// Fail only candidate reads, after the fresh owner read and
				// execution-order filtering have completed.
				if (name == "first" && gets[name] == 3) || (name == "second" && gets[name] == 2) {
					return true, nil, apierrors.NewServiceUnavailable("transient candidate read")
				}
				return false, nil, nil
			})
			assert.ErrorContains(t, f.r.ReconcileKind(f.ctx, f.owner), "transient candidate read")
			assert.Equal(t, gets["first"], 3)
			assert.Equal(t, gets["second"], 2)
			assert.Equal(t, len(f.qm.RunningPipelineRuns(f.repo)), 0)
			assert.Equal(t, len(f.qm.QueuedPipelineRuns(f.repo)), 3)

			configRepo = configRepo.DeepCopy()
			limit := 1
			configRepo.Spec.ConcurrencyLimit = &limit
			assert.NilError(t, f.repoIndexer.Update(configRepo))
			err := f.r.ReconcileKind(f.ctx, f.get(t, "first"))
			assert.ErrorContains(t, err, "unfinished queue admission")
			assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "only reporting may fail for the started candidate: %v", err)
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
			assert.Equal(t, string(f.get(t, "first").Spec.Status), "")
			for _, name := range []string{"second", "third"} {
				pr := f.get(t, name)
				assert.Equal(t, pr.Spec.Status, tektonv1.PipelineRunSpecStatus(tektonv1.PipelineRunSpecStatusPending))
				assert.Equal(t, pr.Annotations[keys.State], kubeinteraction.StateQueued)
			}
			assert.ErrorContains(t, f.r.ReconcileKind(f.ctx, f.get(t, "first")), "waiting to resume")
			if tt.inherited {
				cached, err := f.r.repoLister.Repositories(f.repo.Namespace).Get(f.repo.Name)
				assert.NilError(t, err)
				assert.Assert(t, cached.Spec.ConcurrencyLimit == nil)
				assert.Assert(t, cached.Spec.GitProvider.Secret == nil, "queue configuration must not change secret inheritance")
			}
		})
	}
}

func TestQueueRecoveryStartConflict(t *testing.T) {
	for _, mode := range []string{"initial", "finalization"} {
		t.Run(mode, func(t *testing.T) {
			f := newQueueRecoveryFixture(t, 1, mode == "initial")
			patches := 0
			patchesAtReadback := 0
			f.cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				patch, ok := action.(k8stesting.PatchAction)
				if !ok || patch.GetName() != "first" {
					return false, nil, nil
				}
				patches++
				if patches == 1 {
					resource := tektonv1.SchemeGroupVersion.WithResource("pipelineruns")
					obj, err := f.cs.Tracker().Get(resource, "ns", "first")
					assert.NilError(t, err)
					pr, ok := obj.(*tektonv1.PipelineRun)
					assert.Assert(t, ok)
					pr.ResourceVersion = "2"
					assert.NilError(t, f.cs.Tracker().Update(resource, pr, "ns"))
				}
				return f.applyPatch(t, action)
			})
			f.cs.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if get, ok := action.(k8stesting.GetAction); ok && get.GetName() == "first" && patches > 0 && patchesAtReadback == 0 {
					patchesAtReadback = patches
				}
				return false, nil, nil
			})
			err := f.call(mode)
			assert.Assert(t, stderrors.Is(err, ErrPipelineRunNotStarted), "got %v", err)
			assert.ErrorContains(t, err, "stale start")
			assert.Equal(t, patchesAtReadback, 1, "a conflict must go straight to readback")
			assert.Equal(t, patches, 1)
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
			assert.Equal(t, f.get(t, "first").Spec.Status, tektonv1.PipelineRunSpecStatus(tektonv1.PipelineRunSpecStatusPending))

			if mode == "initial" {
				err = f.r.ReconcileKind(f.ctx, f.get(t, "first"))
				assert.Assert(t, stderrors.Is(err, ErrProviderNotConfigured), "only reporting may fail after a fresh retry: %v", err)
			} else {
				assert.NilError(t, f.call(mode))
			}
			assert.Equal(t, patches, 2)
			started := f.get(t, "first")
			assert.Equal(t, started.ResourceVersion, "3")
			assert.Equal(t, string(started.Spec.Status), "")
			assert.Equal(t, started.Annotations[keys.State], kubeinteraction.StateStarted)
			assert.DeepEqual(t, f.qm.RunningPipelineRuns(f.repo), []string{"ns/first"})
			assert.Equal(t, f.get(t, "second").Annotations[keys.State], kubeinteraction.StateQueued)
		})
	}
}
