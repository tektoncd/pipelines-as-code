package queue

import (
	"time"
)

type Semaphore interface {
	acquireLatest() string
	release(string) bool
	resize(int) bool
	addToQueue(string, time.Time) bool
	addToPendingQueue(string, time.Time) bool
	removeFromQueue(string)
	getLimit() int
	getCurrentRunning() []string
	getCurrentPending() []string
}
