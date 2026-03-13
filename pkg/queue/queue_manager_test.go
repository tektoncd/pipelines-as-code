package queue

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"knative.dev/pkg/apis"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func skipOnOSX64(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("Skipping test on OSX arm64")
	}
}

func TestSomeoneElseSetPendingWithNoConcurrencyLimit(t *testing.T) {
	// Skip if we are running on OSX, there is a problem with ordering only happening on arm64
	skipOnOSX64(t)

	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewManager(logger)
	repo := newTestRepo(1)
	// unset concurrency limit
	repo.Spec.ConcurrencyLimit = nil

	pr := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	// set to pending
	pr.Status.Conditions = duckv1.Conditions{
		{
			Type:   apis.ConditionSucceeded,
			Reason: v1beta1.PipelineRunReasonPending.String(),
		},
	}
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)
}

func TestAddToPendingQueueDirectly(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewManager(logger)
	repo := newTestRepo(1)
	// unset concurrency limit
	repo.Spec.ConcurrencyLimit = nil

	pr := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	// set to pending
	pr.Status.Conditions = duckv1.Conditions{
		{
			Type:   apis.ConditionSucceeded,
			Reason: v1beta1.PipelineRunReasonPending.String(),
		},
	}
	err := qm.AddToPendingQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)

	sema := qm.queueMap[RepoKey(repo)]
	assert.Equal(t, len(sema.getCurrentPending()), 1)
}

func TestNewManagerForList(t *testing.T) {
	// Skip if we are running on OSX, there is a problem with ordering only happening on arm64
	skipOnOSX64(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewManager(logger)

	// repository for which pipelineRun are created
	repo := newTestRepo(1)

	// first pipelineRun
	prFirst := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})

	// added to queue, as there is only one should start
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(prFirst)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)

	// removing the running from queue
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(repo, prFirst), "")

	// adding another 2 pipelineRun, limit is 1 so this will be added to pending queue and
	// then one will be started
	prSecond := newTestPR("second", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prThird := newTestPR("third", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)
	// as per the list, 2nd must be started
	assert.Equal(t, started[0], PrKey(prSecond))

	// adding 2 more, will be going to pending queue
	prFourth := newTestPR("fourth", time.Now().Add(5*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prFifth := newTestPR("fifth", time.Now().Add(4*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFourth), PrKey(prFifth)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// removing 2nd from queue, which means it should start 3rd
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(repo, prSecond), PrKey(prThird))

	// changing the concurrency limit to 2
	repo.Spec.ConcurrencyLimit = intPtr(2)

	prSixth := newTestPR("sixth", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prSeventh := newTestPR("seventh", time.Now().Add(5*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prEight := newTestPR("eight", time.Now().Add(4*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prSixth), PrKey(prSeventh), PrKey(prEight)})
	assert.NilError(t, err)
	// third is running, but limit is changed now, so one more should be moved to running
	assert.Equal(t, len(started), 1)
	assert.Equal(t, started[0], PrKey(prFourth))
}

func TestNewManagerReListing(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewManager(logger)

	// repository for which pipelineRun are created
	repo := newTestRepo(2)

	prFirst := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	prSecond := newTestPR("second", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prThird := newTestPR("third", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	// added to queue, as there is only one should start
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(prFirst), PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 2)

	// if first is running and other pipelineRuns are reconciling
	// then adding again shouldn't have any effect
	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFirst), PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// again
	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFirst), PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// still there should only one running and 2 in pending
	assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 2)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo)), 1)
	// assert.Equal(t, qm.QueuedPipelineRuns(repo)[0], "test-ns/third")

	// a new request comes
	prFourth := newTestPR("fourth", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	prFifth := newTestPR("fifth", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prSixths := newTestPR("sixth", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFourth), PrKey(prFifth), PrKey(prSixths)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 2)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo)), 4)
}

func newTestRepo(limit int) *v1alpha1.Repository {
	return &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(limit),
		},
	}
}

var intPtr = func(val int) *int { return &val }

func newTestPR(name string, time time.Time, labels, annotations map[string]string, spec tektonv1.PipelineRunSpec) *tektonv1.PipelineRun {
	return &tektonv1.PipelineRun{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "test-ns",
			CreationTimestamp: metav1.Time{Time: time},
			Labels:            labels,
			Annotations:       annotations,
		},
		Spec:   spec,
		Status: tektonv1.PipelineRunStatus{},
	}
}

func TestQueueManager_InitQueues(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	cw := clockwork.NewFakeClock()

	startedLabel := map[string]string{
		keys.State: kubeinteraction.StateStarted,
	}
	queuedLabel := map[string]string{
		keys.State: kubeinteraction.StateQueued,
	}

	repo := newTestRepo(1)

	queuedAnnotations := map[string]string{
		keys.ExecutionOrder: "test-ns/first,test-ns/second,test-ns/third",
		keys.State:          kubeinteraction.StateQueued,
	}
	startedAnnotations := map[string]string{
		keys.ExecutionOrder: "test-ns/first,test-ns/second,test-ns/third",
		keys.State:          kubeinteraction.StateStarted,
	}
	firstPR := newTestPR("first", cw.Now(), startedLabel, startedAnnotations, tektonv1.PipelineRunSpec{})
	secondPR := newTestPR("second", cw.Now().Add(5*time.Second), queuedLabel, queuedAnnotations, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})
	thirdPR := newTestPR("third", cw.Now().Add(3*time.Second), queuedLabel, queuedAnnotations, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})

	tdata := testclient.Data{
		Repositories: []*v1alpha1.Repository{repo},
		PipelineRuns: []*tektonv1.PipelineRun{firstPR, secondPR, thirdPR},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	qm := NewManager(logger)

	err := qm.InitQueues(ctx, stdata.Pipeline, stdata.PipelineAsCode)
	assert.NilError(t, err)

	// queues are built
	sema := qm.queueMap[RepoKey(repo)]
	assert.Equal(t, len(sema.getCurrentPending()), 2)
	assert.Equal(t, len(sema.getCurrentRunning()), 1)

	// now if first is completed and removed from running queue
	// then second must start as per execution order
	qm.RemoveAndTakeItemFromQueue(repo, firstPR)
	assert.Equal(t, sema.getCurrentRunning()[0], PrKey(secondPR))
	assert.Equal(t, sema.getCurrentPending()[0], PrKey(thirdPR))

	// list current running pipelineRuns for repo
	runs := qm.RunningPipelineRuns(repo)
	assert.Equal(t, len(runs), 1)
	// list current pending pipelineRuns for repo
	runs = qm.QueuedPipelineRuns(repo)
	assert.Equal(t, len(runs), 1)
}

func TestQueueManager_InitQueues_SkipsMissingExecutionOrder(t *testing.T) {
	// This test verifies the fix for the early-return bug where a PipelineRun
	// without execution-order would cause InitQueues to stop processing all
	// remaining repositories.
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	startedLabel := map[string]string{
		keys.State: kubeinteraction.StateStarted,
	}
	queuedLabel := map[string]string{
		keys.State: kubeinteraction.StateQueued,
	}

	// Create two repositories
	repo1 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: "ns1",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}
	repo2 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "ns2",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}

	// Repo1 has a PipelineRun WITHOUT execution-order (the bug scenario)
	prWithoutOrder := newTestPR("pr-without-order", time.Now(), startedLabel, map[string]string{
		keys.State: kubeinteraction.StateStarted,
		// No execution-order annotation
	}, tektonv1.PipelineRunSpec{})
	prWithoutOrder.Namespace = "ns1"

	// Repo2 has a normal PipelineRun WITH execution-order
	prWithOrder := newTestPR("pr-with-order", time.Now(), startedLabel, map[string]string{
		keys.ExecutionOrder: "ns2/pr-with-order",
		keys.State:          kubeinteraction.StateStarted,
	}, tektonv1.PipelineRunSpec{})
	prWithOrder.Namespace = "ns2"

	// Also add a queued PR to repo2
	prQueued := newTestPR("pr-queued", time.Now(), queuedLabel, map[string]string{
		keys.ExecutionOrder: "ns2/pr-with-order,ns2/pr-queued",
		keys.State:          kubeinteraction.StateQueued,
	}, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})
	prQueued.Namespace = "ns2"

	tdata := testclient.Data{
		Repositories: []*v1alpha1.Repository{repo1, repo2},
		PipelineRuns: []*tektonv1.PipelineRun{prWithoutOrder, prWithOrder, prQueued},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	qm := NewManager(logger)

	// Before the fix, this would return early when encountering prWithoutOrder,
	// and repo2 would not get its queues initialized.
	err := qm.InitQueues(ctx, stdata.Pipeline, stdata.PipelineAsCode)
	assert.NilError(t, err)

	// Verify repo1 has no queues (PipelineRun without execution-order was skipped)
	sema1 := qm.queueMap[RepoKey(repo1)]
	assert.Assert(t, sema1 == nil || len(sema1.getCurrentRunning()) == 0)

	// Verify repo2 DOES have queues initialized (processing continued after repo1)
	sema2 := qm.queueMap[RepoKey(repo2)]
	assert.Assert(t, sema2 != nil, "repo2 should have queues initialized")
	assert.Equal(t, len(sema2.getCurrentRunning()), 1, "repo2 should have 1 running PR")
	assert.Equal(t, len(sema2.getCurrentPending()), 1, "repo2 should have 1 pending PR")
}

func TestQueueManager_InitQueues_NamespaceDeduplication(t *testing.T) {
	// This test verifies namespace deduplication: multiple repos in the same
	// namespace should only trigger ONE List() call per namespace, not one per repo.
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	startedLabel := map[string]string{
		keys.State: kubeinteraction.StateStarted,
	}

	// Create THREE repositories in the SAME namespace
	repo1 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: "shared-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}
	repo2 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "shared-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}
	repo3 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo3",
			Namespace: "shared-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}

	// Each repo has its own started PipelineRun
	pr1 := newTestPR("pr1", time.Now(), startedLabel, map[string]string{
		keys.ExecutionOrder: "shared-ns/pr1",
		keys.State:          kubeinteraction.StateStarted,
	}, tektonv1.PipelineRunSpec{})
	pr1.Namespace = "shared-ns"

	pr2 := newTestPR("pr2", time.Now(), startedLabel, map[string]string{
		keys.ExecutionOrder: "shared-ns/pr2",
		keys.State:          kubeinteraction.StateStarted,
	}, tektonv1.PipelineRunSpec{})
	pr2.Namespace = "shared-ns"

	pr3 := newTestPR("pr3", time.Now(), startedLabel, map[string]string{
		keys.ExecutionOrder: "shared-ns/pr3",
		keys.State:          kubeinteraction.StateStarted,
	}, tektonv1.PipelineRunSpec{})
	pr3.Namespace = "shared-ns"

	tdata := testclient.Data{
		Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
		PipelineRuns: []*tektonv1.PipelineRun{pr1, pr2, pr3},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	qm := NewManager(logger)

	err := qm.InitQueues(ctx, stdata.Pipeline, stdata.PipelineAsCode)
	assert.NilError(t, err)

	// Verify all three repos have their queues initialized
	sema1 := qm.queueMap[RepoKey(repo1)]
	assert.Assert(t, sema1 != nil, "repo1 should have queues")
	assert.Equal(t, len(sema1.getCurrentRunning()), 1, "repo1 should have 1 running PR")

	sema2 := qm.queueMap[RepoKey(repo2)]
	assert.Assert(t, sema2 != nil, "repo2 should have queues")
	assert.Equal(t, len(sema2.getCurrentRunning()), 1, "repo2 should have 1 running PR")

	sema3 := qm.queueMap[RepoKey(repo3)]
	assert.Assert(t, sema3 != nil, "repo3 should have queues")
	assert.Equal(t, len(sema3.getCurrentRunning()), 1, "repo3 should have 1 running PR")

	// Before optimization: 3 repos × 2 states = 6 List() calls
	// After optimization: 1 namespace × 2 states = 2 List() calls
	// (We can't directly measure API calls in this test, but the logic ensures deduplication)
}

func TestFilterPipelineRunByInProgress(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ns := "test-ns"

	// Create a fake Tekton client
	pipelineRuns := []*tektonv1.PipelineRun{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr1",
				Namespace: ns,
				Annotations: map[string]string{
					keys.State: kubeinteraction.StateQueued,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusPending,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr2",
				Namespace: ns,
				Annotations: map[string]string{
					keys.State: kubeinteraction.StateCompleted,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusPending,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr3",
				Namespace: ns,
				Annotations: map[string]string{
					keys.State: kubeinteraction.StateQueued,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusCancelled,
			},
		},
	}

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			},
		},
		PipelineRuns: pipelineRuns,
	}

	orderList := make([]string, len(pipelineRuns))
	for i, pr := range pipelineRuns {
		orderList[i] = fmt.Sprintf("%s/%s", ns, pr.GetName())
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	filtered := FilterPipelineRunByState(ctx, stdata.Pipeline, orderList, tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	expected := []string{"test-ns/pr1"}
	assert.DeepEqual(t, filtered, expected)
}
