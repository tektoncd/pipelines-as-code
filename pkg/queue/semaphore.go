package queue

import (
	"sync"
	"time"

	sema "golang.org/x/sync/semaphore"
)

type prioritySemaphore struct {
	name      string
	limit     int
	pending   *priorityQueue
	running   map[string]bool
	semaphore *sema.Weighted
	lock      *sync.Mutex
}

var _ Semaphore = &prioritySemaphore{}

func newSemaphore(name string, limit int) *prioritySemaphore {
	return &prioritySemaphore{
		name:      name,
		limit:     limit,
		pending:   &priorityQueue{itemByKey: make(map[string]*item)},
		semaphore: sema.NewWeighted(int64(limit)),
		running:   make(map[string]bool),
		lock:      &sync.Mutex{},
	}
}

func (s *prioritySemaphore) getLimit() int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.limit
}

func (s *prioritySemaphore) getCurrentPending() []string {
	s.lock.Lock()
	defer s.lock.Unlock()

	keys := make([]string, 0, len(s.pending.items))
	for _, item := range s.pending.items {
		keys = append(keys, item.key)
	}
	return keys
}

func (s *prioritySemaphore) getCurrentRunning() []string {
	s.lock.Lock()
	defer s.lock.Unlock()

	keys := make([]string, 0, len(s.running))
	for k := range s.running {
		keys = append(keys, k)
	}
	return keys
}

func (s *prioritySemaphore) resize(n int) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	cur := len(s.running)
	// downward case, acquired n locks
	if cur > n {
		cur = n
	}

	semaphore := sema.NewWeighted(int64(n))
	status := semaphore.TryAcquire(int64(cur))
	if status {
		s.semaphore = semaphore
		s.limit = n
	}
	return status
}

func (s *prioritySemaphore) removeFromQueue(key string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.pending.remove(key)
}

func (s *prioritySemaphore) addToPendingQueue(key string, creationTime time.Time) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.running[key]; ok {
		return false
	}
	if s.pending.isPending(key) {
		return false
	}
	s.pending.add(key, creationTime.UnixNano())
	return true
}

func (s *prioritySemaphore) acquireLatest() string {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.pending.Len() == 0 {
		return ""
	}

	ready := s.pending.peek()

	if s.semaphore.TryAcquire(1) {
		_ = s.pending.pop()
		s.running[ready.key] = true
		return ready.key
	}
	return ""
}

func (s *prioritySemaphore) release(key string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.running[key]; ok {
		delete(s.running, key)

		// When semaphore resized downward
		// Remove the excess holders from map once the done.
		if len(s.running) >= s.limit {
			return true
		}

		s.semaphore.Release(1)
	}
	return true
}

func (s *prioritySemaphore) addToQueue(key string, creationTime time.Time) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.running[key]; ok {
		return false
	}
	if s.pending.isPending(key) {
		return false
	}
	s.pending.add(key, creationTime.UnixNano())
	return true
}
