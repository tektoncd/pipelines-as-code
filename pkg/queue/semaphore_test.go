package queue

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"gotest.tools/v3/assert"
)

func TestNewSemaphore(t *testing.T) {
	repo := newSemaphore("test", 1)
	cw := clockwork.NewFakeClock()

	assert.Equal(t, repo.getLimit(), 1)

	// add elements
	// randomly adding elements, the element with the less priority
	// must execute first
	assert.Equal(t, repo.addToQueue("C", cw.Now().Add(5*time.Second)), true)
	assert.Equal(t, repo.addToQueue("A", cw.Now()), true)
	assert.Equal(t, repo.addToQueue("B", cw.Now().Add(1*time.Second)), true)

	// start the topmost, which would be A
	assert.Equal(t, repo.acquireLatest(), "A")

	// limit is 1, so B and C stay pending
	assert.Equal(t, repo.acquireLatest(), "")

	assert.Equal(t, len(repo.getCurrentRunning()), 1)
	assert.Equal(t, len(repo.getCurrentPending()), 2)

	// adding element to Queue which is running
	// nothing should happen
	assert.Equal(t, repo.addToQueue("A", cw.Now().Add(5*time.Second)), false)

	// A is done
	repo.release("A")
	repo.removeFromQueue("A")

	assert.Equal(t, len(repo.getCurrentRunning()), 0)
	assert.Equal(t, len(repo.getCurrentPending()), 2)

	// start the next
	assert.Equal(t, repo.acquireLatest(), "B")

	assert.Equal(t, len(repo.getCurrentRunning()), 1)
	assert.Equal(t, len(repo.getCurrentPending()), 1)

	// B is done
	repo.release("B")
	repo.removeFromQueue("B")

	// resize to 2
	repo.resize(2)

	// now add new elements
	assert.Equal(t, repo.addToQueue("D", cw.Now().Add(8*time.Second)), true)
	assert.Equal(t, repo.addToQueue("E", cw.Now().Add(6*time.Second)), true)
	assert.Equal(t, repo.addToQueue("F", cw.Now().Add(7*time.Second)), true)

	// queue already have C in it
	// now the queue must have C > E > F > D
	// C being on the top

	// start the next
	assert.Equal(t, repo.acquireLatest(), "C")
	assert.Equal(t, repo.acquireLatest(), "E")

	// size is 2 now if we try to acquire again it will return empty
	assert.Equal(t, repo.acquireLatest(), "")

	// resize back to 1
	repo.resize(1)
	assert.Equal(t, repo.getLimit(), 1)

	assert.Equal(t, len(repo.getCurrentRunning()), 2)
	assert.Equal(t, len(repo.getCurrentPending()), 2)

	// try to start next but it shouldn't as 2 are still running
	assert.Equal(t, repo.acquireLatest(), "")

	repo.release("C")
	repo.removeFromQueue("C")

	// try again to start next but it shouldn't as 1 is
	// still running
	assert.Equal(t, repo.acquireLatest(), "")

	repo.release("E")
	repo.removeFromQueue("E")

	// empty the pending Queue
	repo.removeFromQueue("F")
	repo.removeFromQueue("D")

	assert.Equal(t, repo.acquireLatest(), "")
}

func TestNewSemaphorePreservesInsertionOrderForEqualPriority(t *testing.T) {
	repo := newSemaphore("test", 1)
	now := time.Unix(123, 0)

	assert.Equal(t, repo.addToQueue("A", now), true)
	assert.Equal(t, repo.addToQueue("B", now), true)
	assert.Equal(t, repo.addToQueue("C", now), true)

	assert.Equal(t, repo.acquireLatest(), "A")
	repo.release("A")
	repo.removeFromQueue("A")

	assert.Equal(t, repo.acquireLatest(), "B")
	repo.release("B")
	repo.removeFromQueue("B")

	assert.Equal(t, repo.acquireLatest(), "C")
}
