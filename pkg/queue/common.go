package queue

import (
	"time"
)

type Semaphore interface {
	acquireLatest() string
	nextPending() string
	requeue(string) bool
	release(string) bool
	resize(int) bool
	addToQueue(string, time.Time) bool
	addToPendingQueue(string, time.Time) bool
	removeFromQueue(string)
	getLimit() int
	getCurrentRunning() []string
	getCurrentPending() []string
}
