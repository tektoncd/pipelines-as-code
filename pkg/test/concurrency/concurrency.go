package concurrency

import (
	"context"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacVersionedClient "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
)

type TestQMI struct {
	QueuedPrs    []string
	RunningQueue []string
	// Removed records the "repoKey|prKey" pairs passed to RemoveFromQueue, so
	// tests can assert whether a queue slot was released.
	Removed *[]string
	// NextInQueue is drained one key per RemoveAndTakeItemFromQueue call, so
	// tests can feed the promotion loop a sequence of candidates.
	NextInQueue *[]string
	// EchoAcquired makes AddListToRunningQueue return exactly the list it was
	// given instead of the fixed RunningQueue, so a test can observe whether a
	// dropped key is still present in the list passed on a later call.
	EchoAcquired bool
	// Passed records the list argument of every AddListToRunningQueue call, in
	// order, so a test can assert what a later retry iteration was given.
	Passed *[][]string
	// Taken counts RemoveAndTakeItemFromQueue calls, so a test can assert the
	// finished PipelineRun's slot was released at least once.
	Taken *int
	// RepeatNext makes RemoveAndTakeItemFromQueue hand out the same key
	// forever, modelling a queue that never releases a slot, so a test can
	// check the caller gives up instead of looping.
	RepeatNext string
}

func (TestQMI) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ pacVersionedClient.Interface) error {
	// TODO implement me
	panic("implement me")
}

func (TestQMI) RemoveRepository(_ *pacv1alpha1.Repository) {
}

func (t TestQMI) QueuedPipelineRuns(_ *pacv1alpha1.Repository) []string {
	return t.QueuedPrs
}

func (TestQMI) RunningPipelineRuns(_ *pacv1alpha1.Repository) []string {
	// TODO implement me
	panic("implement me")
}

func (t TestQMI) AddListToRunningQueue(_ *pacv1alpha1.Repository, list []string) ([]string, error) {
	if t.Passed != nil {
		*t.Passed = append(*t.Passed, append([]string{}, list...))
	}
	if t.EchoAcquired {
		return list, nil
	}
	return t.RunningQueue, nil
}

func (TestQMI) AddToPendingQueue(_ *pacv1alpha1.Repository, _ []string) error {
	// TODO implement me
	panic("implement me")
}

func (t TestQMI) RemoveFromQueue(repoKey, prKey string) bool {
	if t.Removed != nil {
		*t.Removed = append(*t.Removed, repoKey+"|"+prKey)
	}
	return false
}

func (t TestQMI) RemoveAndTakeItemFromQueue(_ *pacv1alpha1.Repository, _ *tektonv1.PipelineRun) string {
	if t.Taken != nil {
		*t.Taken++
	}
	if t.RepeatNext != "" {
		return t.RepeatNext
	}
	if t.NextInQueue == nil || len(*t.NextInQueue) == 0 {
		return ""
	}
	next := (*t.NextInQueue)[0]
	*t.NextInQueue = (*t.NextInQueue)[1:]
	return next
}
