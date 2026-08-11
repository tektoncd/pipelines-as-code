package reconciler

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testconcurrency "github.com/openshift-pipelines/pipelines-as-code/pkg/test/concurrency"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	fakepipelineclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestQueuePipelineRun(t *testing.T) {
	tests := []struct {
		name          string
		wantErrString string
		wantLog       string
		pipelineRun   *tektonv1.PipelineRun
		testRepo      *pacv1alpha1.Repository
		globalRepo    *pacv1alpha1.Repository
		runningQueue  []string
	}{
		{
			name: "no existing order annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "no repo name annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
					},
				},
			},
			wantErrString: fmt.Sprintf("no %s annotation found", keys.Repository),
		},
		{
			name: "empty repo name annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "",
					},
				},
			},
			wantErrString: fmt.Sprintf("annotation %s is empty", keys.Repository),
		},
		{
			name: "no repo found",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "foo",
					},
				},
			},
		},
		{
			name: "merging global repository settings",
			globalRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "global",
					Namespace: "global",
				},
				Spec: pacv1alpha1.RepositorySpec{
					Settings: &pacv1alpha1.Settings{
						PipelineRunProvenance: "somewhere",
					},
				},
			},
			runningQueue: []string{},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog: "Merging global repository settings with local repository settings",
		},
		{
			name:         "no new PR acquired",
			runningQueue: []string{},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog: "no new PipelineRun acquired for repo test",
		},
		{
			name:         "failed to get PR from the Q after many iterations",
			runningQueue: []string{"test/test2"},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog:       "failed to get PR",
			wantErrString: "max iterations reached of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, logcatch := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			repos := []*pacv1alpha1.Repository{}
			if tt.testRepo != nil {
				repos = append(repos, tt.testRepo)
			}
			if tt.globalRepo != nil {
				repos = append(repos, tt.globalRepo)
			}
			testData := testclient.Data{
				Repositories: repos,
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			r := &Reconciler{
				qm: testconcurrency.TestQMI{
					RunningQueue: tt.runningQueue,
				},
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Info: info.Info{
						Kube: &info.KubeOpts{
							Namespace: "global",
						},
						Controller: &info.ControllerInfo{},
					},
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            fakelogger,
					},
				},
			}
			if tt.globalRepo != nil {
				r.run.Info.Controller.GlobalRepository = tt.globalRepo.GetName()
			}
			err := r.queuePipelineRun(ctx, fakelogger, tt.pipelineRun)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)

			if tt.wantLog != "" {
				assert.Assert(t, logcatch.FilterMessage(tt.wantLog).Len() != 0, "We didn't get the expected log message", logcatch.All())
			}
			if tt.globalRepo != nil && tt.testRepo != nil {
				cachedRepo, err := informers.Repository.Lister().Repositories(tt.testRepo.Namespace).Get(tt.testRepo.Name)
				assert.NilError(t, err)
				assert.Assert(t, cachedRepo.Spec.Settings == nil, "global settings should not mutate the cached Repository")
			}
		})
	}
}

// TestQueuePipelineRunSlotRelease asserts when a queue slot is released and,
// more importantly, when it must not be. The slot follows the state patch: it
// is what clears spec.status, so before it lands the PipelineRun is still
// pending and holding its slot would strand it forever, and after it lands the
// PipelineRun is running and releasing its slot would admit past the limit.
func TestQueuePipelineRunSlotRelease(t *testing.T) {
	const ns = "test"

	acquiredPR := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "queued",
			Namespace: ns,
			Annotations: map[string]string{
				keys.Repository: "test",
				keys.State:      "queued",
			},
		},
		Spec: tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusPending},
	}

	tests := []struct {
		name          string
		seededPR      *tektonv1.PipelineRun
		setup         func(t *testing.T, cs *fakepipelineclientset.Clientset)
		wantErrString string
		wantReleased  bool
	}{
		{
			name:          "start failure after the state patch keeps the queue slot",
			seededPR:      acquiredPR,
			wantErrString: "failed to update pipelineRun test/queued to in_progress",
			wantReleased:  false,
		},
		{
			name:          "vanished pipelineRun releases the queue slot",
			seededPR:      nil,
			wantErrString: "max iterations reached of",
			wantReleased:  true,
		},
		{
			name:     "failing state patch releases the queue slot",
			seededPR: acquiredPR,
			setup: func(t *testing.T, cs *fakepipelineclientset.Clientset) {
				t.Helper()
				cs.PrependReactor("patch", "pipelineruns", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewInternalError(fmt.Errorf("etcdserver: request timed out"))
				})
			},
			wantErrString: "failed to update pipelineRun test/queued to in_progress",
			wantReleased:  true,
		},
		{
			name:     "transient get failure releases the queue slot",
			seededPR: acquiredPR,
			setup: func(t *testing.T, cs *fakepipelineclientset.Clientset) {
				t.Helper()
				cs.PrependReactor("get", "pipelineruns", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewServiceUnavailable("apiserver is shutting down")
				})
			},
			wantErrString: "failed to get pipelineRun test/queued",
			wantReleased:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			repo := &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: ns},
				Spec:       pacv1alpha1.RepositorySpec{URL: "https://psg.fr"},
			}
			testData := testclient.Data{Repositories: []*pacv1alpha1.Repository{repo}}
			if tt.seededPR != nil {
				testData.PipelineRuns = []*tektonv1.PipelineRun{tt.seededPR}
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			if tt.setup != nil {
				tt.setup(t, stdata.Pipeline)
			}

			released := []string{}
			r := &Reconciler{
				qm: testconcurrency.TestQMI{
					RunningQueue: []string{ns + "/queued"},
					Removed:      &released,
				},
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{},
						Pac:        &info.PacOpts{},
					},
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            fakelogger,
					},
				},
			}

			trigger := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trigger",
					Namespace: ns,
					Annotations: map[string]string{
						keys.ExecutionOrder: ns + "/queued",
						keys.Repository:     "test",
					},
				},
			}

			err := r.queuePipelineRun(ctx, fakelogger, trigger)
			assert.ErrorContains(t, err, tt.wantErrString)
			if tt.wantReleased {
				assert.Assert(t, len(released) > 0, "expected the queue slot to be released, got none")
			} else {
				assert.Assert(t, len(released) == 0, "queue slot must not be released, got %v", released)
			}
		})
	}
}

// TestQueuePipelineRunProcessesAllAcquiredSlots asserts that a failure on one
// acquired PipelineRun does not abandon the others. With a concurrency limit
// above one, several PipelineRuns can be acquired in the same call; each one
// already holds a slot, so every one of them must end up either started or
// released. Returning early would strand the rest in the running set forever.
func TestQueuePipelineRunProcessesAllAcquiredSlots(t *testing.T) {
	const ns = "test"

	makePR := func(name string) *tektonv1.PipelineRun {
		return &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Annotations: map[string]string{
					keys.Repository: "test",
					keys.State:      "queued",
				},
			},
			Spec: tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusPending},
		}
	}

	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: ns},
		Spec:       pacv1alpha1.RepositorySpec{URL: "https://psg.fr"},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*pacv1alpha1.Repository{repo},
		PipelineRuns: []*tektonv1.PipelineRun{makePR("first"), makePR("second")},
	})

	// The state patch fails only for "first"; "second" must still be processed.
	stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if patch, ok := action.(k8stesting.PatchAction); ok && patch.GetName() == "first" {
			return true, nil, apierrors.NewInternalError(fmt.Errorf("etcdserver: request timed out"))
		}
		return false, nil, nil
	})

	released := []string{}
	r := &Reconciler{
		qm: testconcurrency.TestQMI{
			RunningQueue: []string{ns + "/first", ns + "/second"},
			Removed:      &released,
		},
		repoLister: informers.Repository.Lister(),
		run: &params.Run{
			Info: info.Info{
				Kube:       &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{},
				Pac:        &info.PacOpts{},
			},
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Tekton:         stdata.Pipeline,
				Kube:           stdata.Kube,
				Log:            fakelogger,
			},
		},
	}

	trigger := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trigger",
			Namespace: ns,
			Annotations: map[string]string{
				keys.ExecutionOrder: ns + "/first," + ns + "/second",
				keys.Repository:     "test",
			},
		},
	}

	err := r.queuePipelineRun(ctx, fakelogger, trigger)
	assert.ErrorContains(t, err, "pipelineRun test/first")

	// "first" never left pending, so its slot must have been handed back.
	assert.DeepEqual(t, released, []string{ns + "/test|" + ns + "/first"})

	// "second" must not have been abandoned: its state patch went through.
	second, err := stdata.Pipeline.TektonV1().PipelineRuns(ns).Get(ctx, "second", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, second.GetAnnotations()[keys.State], "started")
	assert.Equal(t, string(second.Spec.Status), "")
}

// TestQueuePipelineRunDropsGoneKeysFromRetry asserts that a PipelineRun which
// no longer exists in the cluster is not retried across iterations of the
// acquire loop. AddListToRunningQueue is called again with the same ordered
// list whenever nothing was processed and no error occurred, so a key
// dropped in one iteration must also be removed from that list, or it comes
// straight back on the next call and gets re-acquired and re-failed until
// maxIterations trips, even though there was never anything wrong.
func TestQueuePipelineRunDropsGoneKeysFromRetry(t *testing.T) {
	const ns = "test"

	makePR := func(name string) *tektonv1.PipelineRun {
		return &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Annotations: map[string]string{
					keys.Repository: "test",
					keys.State:      "queued",
				},
			},
			Spec: tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusPending},
		}
	}

	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: ns},
		Spec:       pacv1alpha1.RepositorySpec{URL: "https://psg.fr"},
	}
	// Seeded so the ordered list built at the top of queuePipelineRun includes
	// them, then made to vanish on the next Get, simulating the real race:
	// present when the list was built, gone by the time the acquire loop tries
	// to fetch it.
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*pacv1alpha1.Repository{repo},
		PipelineRuns: []*tektonv1.PipelineRun{makePR("gone1"), makePR("gone2")},
	})

	gets := map[string]int{}
	stdata.Pipeline.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get, ok := action.(k8stesting.GetAction)
		if !ok {
			return false, nil, nil
		}
		gets[get.GetName()]++
		if gets[get.GetName()] > 1 {
			return true, nil, apierrors.NewNotFound(tektonv1.Resource("pipelineruns"), get.GetName())
		}
		return false, nil, nil
	})

	passed := [][]string{}
	r := &Reconciler{
		qm: testconcurrency.TestQMI{
			EchoAcquired: true,
			Passed:       &passed,
		},
		repoLister: informers.Repository.Lister(),
		run: &params.Run{
			Info: info.Info{
				Kube:       &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{},
				Pac:        &info.PacOpts{},
			},
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Tekton:         stdata.Pipeline,
				Kube:           stdata.Kube,
				Log:            fakelogger,
			},
		},
	}

	trigger := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trigger",
			Namespace: ns,
			Annotations: map[string]string{
				keys.ExecutionOrder: ns + "/gone1," + ns + "/gone2",
				keys.Repository:     "test",
			},
		},
	}

	err := r.queuePipelineRun(ctx, fakelogger, trigger)
	// Both keys are legitimately gone: there is nothing left to do, so this
	// must not surface as a "max iterations reached" error.
	assert.NilError(t, err)

	// The first call is handed the full ordered list; once both keys 404 and
	// are dropped, the retry must be handed an empty list instead of the same
	// two gone keys again.
	assert.Assert(t, len(passed) >= 2, "expected at least two AddListToRunningQueue calls, got %d", len(passed))
	assert.DeepEqual(t, passed[0], []string{ns + "/gone1", ns + "/gone2"})
	assert.Equal(t, len(passed[1]), 0, "gone keys must not be retried on the next iteration")
}

// TestStartNextPipelineRunInQueue asserts the completion path obeys the same
// slot rule as the acquire path: a candidate that never left pending gives its
// slot back and the queue moves on to the next one, while a candidate whose
// start failed only after the state patch keeps its slot.
func TestStartNextPipelineRunInQueue(t *testing.T) {
	const ns = "test"

	makePR := func(name string) *tektonv1.PipelineRun {
		return &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Annotations: map[string]string{
					keys.Repository: "test",
					keys.State:      "queued",
				},
			},
			Spec: tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusPending},
		}
	}

	tests := []struct {
		name           string
		nextInQueue    []string
		seeded         []*tektonv1.PipelineRun
		setup          func(t *testing.T, cs *fakepipelineclientset.Clientset)
		wantReleased   []string
		wantStarted    []string
		wantNotStarted []string
	}{
		{
			name:        "vanished candidate releases its slot and the next one starts",
			nextInQueue: []string{ns + "/gone", ns + "/second"},
			seeded:      []*tektonv1.PipelineRun{makePR("second")},
			wantReleased: []string{
				ns + "/test|" + ns + "/gone",
			},
			wantStarted: []string{"second"},
		},
		{
			name:        "failing state patch releases the slot and the next one starts",
			nextInQueue: []string{ns + "/first", ns + "/second"},
			seeded:      []*tektonv1.PipelineRun{makePR("first"), makePR("second")},
			setup: func(t *testing.T, cs *fakepipelineclientset.Clientset) {
				t.Helper()
				cs.PrependReactor("patch", "pipelineruns", func(action k8stesting.Action) (bool, runtime.Object, error) {
					if patch, ok := action.(k8stesting.PatchAction); ok && patch.GetName() == "first" {
						return true, nil, apierrors.NewInternalError(fmt.Errorf("etcdserver: request timed out"))
					}
					return false, nil, nil
				})
			},
			wantReleased: []string{
				ns + "/test|" + ns + "/first",
			},
			wantStarted: []string{"second"},
		},
		{
			// The state patch on "first" succeeds, then provider detection
			// fails, which is a post-patch failure: "first" is running and owns
			// its slot, so nothing is released and "second" is not promoted.
			name:           "start failure after the state patch keeps the slot",
			nextInQueue:    []string{ns + "/first", ns + "/second"},
			seeded:         []*tektonv1.PipelineRun{makePR("first"), makePR("second")},
			wantReleased:   nil,
			wantStarted:    []string{"first"},
			wantNotStarted: []string{"second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			repo := &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: ns},
				Spec:       pacv1alpha1.RepositorySpec{URL: "https://psg.fr"},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				Repositories: []*pacv1alpha1.Repository{repo},
				PipelineRuns: tt.seeded,
			})
			if tt.setup != nil {
				tt.setup(t, stdata.Pipeline)
			}

			released := []string{}
			next := append([]string{}, tt.nextInQueue...)
			r := &Reconciler{
				qm: testconcurrency.TestQMI{
					Removed:     &released,
					NextInQueue: &next,
				},
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{},
						Pac:        &info.PacOpts{},
					},
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            fakelogger,
					},
				},
			}

			finished := makePR("finished")
			r.startNextPipelineRunInQueue(ctx, fakelogger, repo, finished)

			assert.DeepEqual(t, released, func() []string {
				if tt.wantReleased == nil {
					return []string{}
				}
				return tt.wantReleased
			}())
			for _, name := range tt.wantStarted {
				got, err := stdata.Pipeline.TektonV1().PipelineRuns(ns).Get(ctx, name, metav1.GetOptions{})
				assert.NilError(t, err)
				assert.Equal(t, got.GetAnnotations()[keys.State], "started", "pipelinerun %s should have been started", name)
			}
			for _, name := range tt.wantNotStarted {
				got, err := stdata.Pipeline.TektonV1().PipelineRuns(ns).Get(ctx, name, metav1.GetOptions{})
				assert.NilError(t, err)
				assert.Equal(t, got.GetAnnotations()[keys.State], "queued", "pipelinerun %s should not have been started", name)
			}
		})
	}
}

// TestStartNextPipelineRunInQueueGivesUp asserts the promotion loop terminates.
// Every candidate that cannot be started hands its slot back and the loop asks
// the queue for another one, so a queue that keeps handing out a key it was
// asked to remove would spin forever and take the whole test binary down with
// it on the -timeout kill. It must give up instead, and it must always release
// the slot it is holding when it does.
func TestStartNextPipelineRunInQueueGivesUp(t *testing.T) {
	const ns = "test"

	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: ns},
		Spec:       pacv1alpha1.RepositorySpec{URL: "https://psg.fr"},
	}

	tests := []struct {
		name        string
		repeatNext  string
		cancel      bool
		wantRemoved []string
	}{
		{
			// The key never resolves to a PipelineRun, so the first iteration
			// releases it and asks for another one, and the queue hands back
			// the very key it was told to remove.
			name:       "a queue that keeps returning a removed key is abandoned",
			repeatNext: ns + "/never-there",
			wantRemoved: []string{
				ns + "/test|" + ns + "/never-there",
				ns + "/test|" + ns + "/never-there",
			},
		},
		{
			// A malformed key would panic on key[1] if it were not guarded.
			name:       "a malformed key is dropped instead of indexed",
			repeatNext: "no-slash-here",
			wantRemoved: []string{
				ns + "/test|no-slash-here",
				ns + "/test|no-slash-here",
			},
		},
		{
			// A cancelled context must still cost exactly one
			// RemoveAndTakeItemFromQueue, or the finished PipelineRun's own
			// slot is never freed, and the slot taken for the candidate we
			// then decline to start must be handed back.
			name:        "a cancelled context releases the slot it just took",
			repeatNext:  ns + "/never-there",
			cancel:      true,
			wantRemoved: []string{ns + "/test|" + ns + "/never-there"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				Repositories: []*pacv1alpha1.Repository{repo},
			})

			removed := []string{}
			taken := 0
			r := &Reconciler{
				qm: testconcurrency.TestQMI{
					Removed:    &removed,
					RepeatNext: tt.repeatNext,
					Taken:      &taken,
				},
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: &info.ControllerInfo{},
						Pac:        &info.PacOpts{},
					},
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            fakelogger,
					},
				},
			}

			finished := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{Name: "finished", Namespace: ns},
			}
			r.startNextPipelineRunInQueue(ctx, fakelogger, repo, finished)

			assert.DeepEqual(t, removed, tt.wantRemoved)
			// RemoveAndTakeItemFromQueue is what frees the finished
			// PipelineRun's slot, so it must run at least once whatever else
			// happens, including on an already-cancelled context.
			assert.Assert(t, taken >= 1, "the finished pipelineRun's slot was never released")
		})
	}
}

// TestStartNextPipelineRunInQueueReleasesSlotForReal drives the promotion loop
// against the real queue.Manager rather than a fake, to prove the slot of a
// vanished candidate is genuinely handed back to the semaphore. TestQMI records
// the calls but its RemoveFromQueue is a no-op, so on its own it cannot show
// that the concurrency limit is actually recovered.
func TestStartNextPipelineRunInQueueReleasesSlotForReal(t *testing.T) {
	const ns = "test"
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	limit := 1
	repo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: ns},
		Spec:       pacv1alpha1.RepositorySpec{URL: "https://psg.fr", ConcurrencyLimit: &limit},
	}

	// "gone" is queued but never seeded into the cluster, so promoting it must
	// fail and give its slot back. "finished" is the completed run whose slot
	// is being freed.
	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*pacv1alpha1.Repository{repo},
	})

	qm := queuepkg.NewManager(fakelogger)
	_, err := qm.AddListToRunningQueue(repo, []string{ns + "/finished"})
	assert.NilError(t, err)
	assert.NilError(t, qm.AddToPendingQueue(repo, []string{ns + "/gone"}))
	assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 1, "the finished run should hold the only slot")

	r := &Reconciler{
		qm:         qm,
		repoLister: informers.Repository.Lister(),
		run: &params.Run{
			Info: info.Info{
				Kube:       &info.KubeOpts{Namespace: "global"},
				Controller: &info.ControllerInfo{},
				Pac:        &info.PacOpts{},
			},
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Tekton:         stdata.Pipeline,
				Kube:           stdata.Kube,
				Log:            fakelogger,
			},
		},
	}

	finished := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Name: "finished", Namespace: ns},
	}
	r.startNextPipelineRunInQueue(ctx, fakelogger, repo, finished)

	// Both the finished run and the vanished candidate must be gone from the
	// queue. If "gone" kept the slot it briefly held, the repository would sit
	// at its limit of 1 with nothing actually running, which is exactly the
	// drift this whole change is about.
	assert.DeepEqual(t, qm.RunningPipelineRuns(repo), []string{})
	assert.DeepEqual(t, qm.QueuedPipelineRuns(repo), []string{})

	// The freed slot must be usable again: a newly queued run gets admitted.
	acquired, err := qm.AddListToRunningQueue(repo, []string{ns + "/fresh"})
	assert.NilError(t, err)
	assert.DeepEqual(t, acquired, []string{ns + "/fresh"})
}
