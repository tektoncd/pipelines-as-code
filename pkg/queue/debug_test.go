package queue

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDebugHandler(t *testing.T) {
	limit := 2
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Spec:       v1alpha1.RepositorySpec{ConcurrencyLimit: &limit},
	}

	tests := []struct {
		name     string
		register func() *Manager
		wantCode int
		want     map[string]RepoQueue
	}{
		{
			name:     "no manager registered yet",
			register: func() *Manager { return nil },
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:     "empty manager",
			register: func() *Manager { return NewManager(zap.NewNop().Sugar()) },
			wantCode: http.StatusOK,
			want:     map[string]RepoQueue{},
		},
		{
			name: "one running and one pending",
			register: func() *Manager {
				qm := NewManager(zap.NewNop().Sugar())
				sema, err := qm.getSemaphore(repo)
				assert.NilError(t, err)
				sema.addToQueue("ns/pr-1", metav1.Now().Time)
				sema.addToQueue("ns/pr-2", metav1.Now().Time)
				sema.addToQueue("ns/pr-3", metav1.Now().Time)
				sema.acquireLatest()
				sema.acquireLatest()
				return qm
			},
			wantCode: http.StatusOK,
			want: map[string]RepoQueue{
				"ns/repo": {Limit: 2, Running: []string{"ns/pr-1", "ns/pr-2"}, Pending: []string{"ns/pr-3"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterForDebug(tt.register())
			t.Cleanup(func() { RegisterForDebug(nil) })

			rec := httptest.NewRecorder()
			DebugHandler()(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/queue", nil))

			assert.Equal(t, rec.Code, tt.wantCode)
			if tt.wantCode != http.StatusOK {
				return
			}
			got := map[string]RepoQueue{}
			assert.NilError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

// TestDebugHandlerDoesNotWaitForTheQueue checks the handler gives up instead of
// blocking when the reconciler holds the lock. Reading the queue must never be
// able to stall the thing being read, so this runs the handler off the test
// goroutine and fails on the timeout rather than hanging the suite.
func TestDebugHandlerDoesNotWaitForTheQueue(t *testing.T) {
	qm := NewManager(zap.NewNop().Sugar())
	qm.lock.Lock()
	defer qm.lock.Unlock()

	RegisterForDebug(qm)
	t.Cleanup(func() { RegisterForDebug(nil) })

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		DebugHandler()(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/queue", nil))
	}()

	select {
	case <-done:
		assert.Equal(t, rec.Code, http.StatusServiceUnavailable)
	case <-time.After(5 * time.Second):
		t.Fatal("the debug handler blocked on the queue lock instead of giving up")
	}
}
