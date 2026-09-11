package queue

import (
	"sync"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestAdmissionOwnedRetries(t *testing.T) {
	tests := []struct {
		name      string
		attempted bool
	}{
		{name: "unread candidate returns to its original queue position"},
		{name: "uncertain start keeps the reservation", attempted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := 1
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"}, Spec: v1alpha1.RepositorySpec{ConcurrencyLimit: &limit}}
			owner := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "finished", Namespace: "ns", UID: "owner"}}
			qm := NewManager(zap.NewNop().Sugar())
			_, err := qm.AddListToRunningQueue(repo, []string{PrKey(owner)})
			assert.NilError(t, err)
			assert.NilError(t, qm.AddToPendingQueue(repo, []string{"ns/first", "ns/second", "ns/third"}))
			work, err := qm.AcquireAdmission(repo, owner, nil, AdmissionPromotion)
			assert.NilError(t, err)
			assert.Equal(t, work.Candidates[0].Key, "ns/first")
			work.Candidates[0].Attempted = tt.attempted
			work.Candidates[0].UID = "first-uid"
			assert.ErrorContains(t, qm.FinishAdmission(work), "unfinished queue admission")
			if tt.attempted {
				assert.DeepEqual(t, qm.RunningPipelineRuns(repo), []string{"ns/first"})
			} else {
				assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 0)
				assert.Equal(t, qm.queueMap[RepoKey(repo)].nextPending(), "ns/first")
			}
			// Other reconciliations must neither steal a retry nor bypass its
			// restored position, even though the semaphore can now be free.
			other := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns"}}
			otherWork, otherErr := qm.AcquireAdmission(repo, other, nil, AdmissionInitial)
			if tt.attempted {
				assert.NilError(t, otherErr)
				assert.Equal(t, len(otherWork.Candidates), 0)
				assert.NilError(t, qm.FinishAdmission(otherWork))
			} else {
				assert.ErrorContains(t, otherErr, "owned by another")
			}
			work, err = qm.AcquireAdmission(repo, owner, nil, AdmissionPromotion)
			assert.NilError(t, err)
			assert.Equal(t, work.Candidates[0].Key, "ns/first")
			assert.Equal(t, string(work.Candidates[0].UID), "first-uid")
			work.Candidates[0].Outcome = StartSucceeded
			assert.NilError(t, qm.FinishAdmission(work))
			assert.Equal(t, len(qm.claims[RepoKey(repo)]), 0)
			assert.Equal(t, qm.queueMap[RepoKey(repo)].nextPending(), "ns/second")
		})
	}
}

func TestAdmissionConcurrentClaims(t *testing.T) {
	tests := []struct {
		name string
		mode AdmissionMode
	}{
		{name: "initial batch", mode: AdmissionInitial},
		{name: "successor handoff", mode: AdmissionPromotion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := 2
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo"}, Spec: v1alpha1.RepositorySpec{ConcurrencyLimit: &limit}}
			owner := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "owner"}}
			qm := NewManager(zap.NewNop().Sugar())
			_, err := qm.AddListToRunningQueue(repo, []string{PrKey(owner)})
			assert.NilError(t, err)
			assert.NilError(t, qm.AddToPendingQueue(repo, []string{"ns/first", "ns/second"}))
			var wg sync.WaitGroup
			results := make(chan *Admission, 16)
			for range 16 {
				wg.Go(func() {
					work, err := qm.AcquireAdmission(repo, owner, nil, tt.mode)
					if err == nil {
						results <- work
					}
				})
			}
			wg.Wait()
			close(results)
			assert.Equal(t, len(results), 1)
			work := <-results
			assert.Equal(t, len(work.Candidates), 1)
			work.Candidates[0].Attempted = true
			assert.ErrorContains(t, qm.FinishAdmission(work), "unfinished queue admission")
			retry, err := qm.AcquireAdmission(repo, owner, nil, tt.mode)
			assert.NilError(t, err)
			assert.Equal(t, retry.Candidates[0].Key, work.Candidates[0].Key)
			retry.Candidates[0].Outcome = StartSucceeded
			assert.NilError(t, qm.FinishAdmission(retry))
		})
	}
}

func TestAdmissionCleanup(t *testing.T) {
	tests := []struct {
		name    string
		cleanup func(*testing.T, *Manager, *v1alpha1.Repository, *tektonv1.PipelineRun)
	}{
		{
			name: "repository removed while worker holds lease",
			cleanup: func(_ *testing.T, qm *Manager, repo *v1alpha1.Repository, _ *tektonv1.PipelineRun) {
				qm.RemoveRepository(repo)
			},
		},
		{
			name: "queues reconstructed while old lease exists",
			cleanup: func(t *testing.T, qm *Manager, _ *v1alpha1.Repository, _ *tektonv1.PipelineRun) {
				t.Helper()
				ctx, _ := rtesting.SetupFakeContext(t)
				data, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
				assert.NilError(t, qm.InitQueues(ctx, data.Pipeline, data.PipelineAsCode))
			},
		},
		{
			name: "candidate completed or cancelled",
			cleanup: func(_ *testing.T, qm *Manager, repo *v1alpha1.Repository, _ *tektonv1.PipelineRun) {
				qm.RemoveFromQueue(RepoKey(repo), "ns/first")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := 1
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo"}, Spec: v1alpha1.RepositorySpec{ConcurrencyLimit: &limit}}
			owner := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "owner"}}
			qm := NewManager(zap.NewNop().Sugar())
			work, err := qm.AcquireAdmission(repo, owner, []string{"ns/first"}, AdmissionInitial)
			assert.NilError(t, err)
			work.Candidates[0].Attempted = true
			tt.cleanup(t, qm, repo, owner)
			assert.NilError(t, qm.FinishAdmission(work))
			assert.Equal(t, len(qm.claims[RepoKey(repo)]), 0)
			assert.Equal(t, len(qm.admissions[RepoKey(repo)]), 0)
		})
	}
}

func TestAdmissionResumesPartialBatchAfterResize(t *testing.T) {
	tests := []struct {
		name        string
		ownerStatus tektonv1.PipelineRunSpecStatus
	}{
		{name: "only acquire the retries that fit"},
		{name: "cancelled owner frees capacity for its batch", ownerStatus: tektonv1.PipelineRunSpecStatusCancelled},
		{name: "gracefully cancelled owner frees capacity for its batch", ownerStatus: tektonv1.PipelineRunSpecStatusCancelledRunFinally},
		{name: "gracefully stopped owner frees capacity for its batch", ownerStatus: tektonv1.PipelineRunSpecStatusStoppedRunFinally},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ownerDone := tt.ownerStatus != ""
			limit := 3
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo"}, Spec: v1alpha1.RepositorySpec{ConcurrencyLimit: &limit}}
			owner := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "ns"}}
			qm := NewManager(zap.NewNop().Sugar())
			list := []string{"ns/first", "ns/second", "ns/third"}
			if ownerDone {
				list[0] = PrKey(owner)
			}
			work, err := qm.AcquireAdmission(repo, owner, list, AdmissionInitial)
			assert.NilError(t, err)
			if ownerDone {
				work.Candidates[0].Outcome = StartSucceeded
				owner.Spec.Status = tt.ownerStatus
			}
			assert.ErrorContains(t, qm.FinishAdmission(work), "unfinished queue admission")
			limit = 1
			work, err = qm.AcquireAdmission(repo, owner, nil, AdmissionResume)
			assert.NilError(t, err)
			assert.Equal(t, len(work.Candidates), 1, "a partial acquisition must be returned, not stranded")
			assert.NilError(t, qm.AddToPendingQueue(repo, []string{"ns/later"}))
			for {
				key := work.Candidates[0].Key
				work.Candidates[0].Outcome = StartSucceeded
				err = qm.FinishAdmission(work)
				qm.RemoveFromQueue(RepoKey(repo), key)
				if err == nil {
					break
				}
				assert.ErrorContains(t, err, "unfinished queue admission")
				work, err = qm.AcquireAdmission(repo, owner, nil, AdmissionResume)
				assert.NilError(t, err)
				assert.Equal(t, len(work.Candidates), 1)
			}
			if ownerDone {
				work, err = qm.AcquireAdmission(repo, owner, nil, AdmissionPromotion)
				assert.NilError(t, err)
				assert.Equal(t, work.Candidates[0].Key, "ns/later", "the early release must still finish its handoff")
				work.Candidates[0].Outcome = StartSucceeded
				assert.NilError(t, qm.FinishAdmission(work))
				qm.ForgetAdmission(RepoKey(repo), owner)
			}
			assert.Equal(t, len(qm.claims[RepoKey(repo)]), 0)
			assert.Equal(t, len(qm.admissions[RepoKey(repo)]), 0)
		})
	}
}

func TestAdmissionResumePreservesReservationsAfterResize(t *testing.T) {
	tests := []struct {
		name string
		held int
	}{
		{name: "uncertain start fills the reduced limit", held: 1},
		{name: "uncertain starts exceed the reduced limit", held: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := 3
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"}, Spec: v1alpha1.RepositorySpec{ConcurrencyLimit: &limit}}
			owner := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "ns"}}
			qm := NewManager(zap.NewNop().Sugar())
			work, err := qm.AcquireAdmission(repo, owner, []string{"ns/first", "ns/second", "ns/third"}, AdmissionInitial)
			assert.NilError(t, err)
			for i := range tt.held {
				work.Candidates[i].Attempted = true
			}
			assert.ErrorContains(t, qm.FinishAdmission(work), "unfinished queue admission")

			limit = 1
			work, err = qm.AcquireAdmission(repo, owner, nil, AdmissionResume)
			assert.NilError(t, err)
			assert.Equal(t, len(work.Candidates), tt.held)
			assert.Equal(t, qm.queueMap[RepoKey(repo)].getLimit(), 1)
			assert.Equal(t, len(qm.RunningPipelineRuns(repo)), tt.held)
			assert.Equal(t, len(qm.QueuedPipelineRuns(repo)), 3-tt.held)
			for i := range work.Candidates {
				assert.Assert(t, work.Candidates[i].Attempted)
				work.Candidates[i].Outcome = StartSucceeded
			}
			assert.ErrorContains(t, qm.FinishAdmission(work), "unfinished queue admission")
			for i, candidate := range work.Candidates {
				_, err := qm.AcquireAdmission(repo, owner, nil, AdmissionResume)
				assert.ErrorContains(t, err, "waiting to resume")
				qm.RemoveFromQueue(RepoKey(repo), candidate.Key)
				assert.Equal(t, len(qm.RunningPipelineRuns(repo)), tt.held-i-1)
			}
			work, err = qm.AcquireAdmission(repo, owner, nil, AdmissionResume)
			assert.NilError(t, err)
			assert.Equal(t, len(work.Candidates), 1)
			assert.Assert(t, !work.Candidates[0].Attempted)
			work.Candidates[0].Outcome = StartGone
			err = qm.FinishAdmission(work)
			if tt.held == 1 {
				assert.ErrorContains(t, err, "unfinished queue admission")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
