package queue

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
)

// RepoQueue is what the manager currently believes about one repository.
type RepoQueue struct {
	Limit   int      `json:"limit"`
	Running []string `json:"running"`
	Pending []string `json:"pending"`
}

// Snapshot returns the state of every queue, keyed by "namespace/repository".
// The bool is false when the manager is busy, in which case there is no
// snapshot to report.
//
// Every concurrency bug so far has been the in-memory queue drifting away from
// what is really in the cluster. Reading the queue directly turns "nothing is
// running and I do not know why" into a named PipelineRun.
//
// This takes the same lock the reconciler needs to admit and release runs, so
// it only ever tries. A diagnostic that can stall the thing it is diagnosing is
// worse than one that occasionally says "ask again".
func (qm *Manager) Snapshot() (map[string]RepoQueue, bool) {
	if !qm.lock.TryLock() {
		return nil, false
	}
	defer qm.lock.Unlock()

	out := make(map[string]RepoQueue, len(qm.queueMap))
	for key, sema := range qm.queueMap {
		running := sema.getCurrentRunning()
		pending := sema.getCurrentPending()
		sort.Strings(running)
		sort.Strings(pending)
		out[key] = RepoQueue{Limit: sema.getLimit(), Running: running, Pending: pending}
	}
	return out, true
}

var (
	debugLock    sync.RWMutex
	debugManager *Manager
)

// RegisterForDebug makes a Manager reachable from DebugHandler. The Manager is
// built long after the watcher's HTTP server is set up, so the handler has to
// find it later rather than be handed it up front.
func RegisterForDebug(qm *Manager) {
	debugLock.Lock()
	defer debugLock.Unlock()
	debugManager = qm
}

// DebugHandler serves a read-only view of the queues as JSON. It mutates
// nothing and exposes only PipelineRun names, which the caller can already list
// from the cluster.
func DebugHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		debugLock.RLock()
		qm := debugManager
		debugLock.RUnlock()

		if qm == nil {
			http.Error(w, "queue manager is not ready yet", http.StatusServiceUnavailable)
			return
		}
		snapshot, ok := qm.Snapshot()
		if !ok {
			http.Error(w, "queue manager is busy, try again", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
