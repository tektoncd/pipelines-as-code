package queue

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"k8s.io/client-go/rest"
)

type ManagerInterface interface {
	InitQueues(ctx context.Context, tekton tektonVersionedClient.Interface, pac versioned.Interface) error
	RemoveRepository(repo *v1alpha1.Repository)
	QueuedPipelineRuns(repo *v1alpha1.Repository) []string
	RunningPipelineRuns(repo *v1alpha1.Repository) []string
	AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error)
	AddToPendingQueue(repo *v1alpha1.Repository, list []string) error
	RemoveFromQueue(repoKey, prKey string) bool
	RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string
	AcquireAdmission(repo *v1alpha1.Repository, owner *tektonv1.PipelineRun, list []string, mode AdmissionMode) (*Admission, error)
	FinishAdmission(work *Admission) error
	ForgetAdmission(repoKey string, owner *tektonv1.PipelineRun)
}

func RepoKey(repo *v1alpha1.Repository) string {
	return fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
}

func PrKey(run *tektonv1.PipelineRun) string {
	return fmt.Sprintf("%s/%s", run.Namespace, run.Name)
}

// SplitPrKey parses a "namespace/name" queue key, the inverse of PrKey.
//
// Keys can come straight from the user-editable execution-order annotation, so
// a malformed entry must be rejected rather than indexed: callers use the
// namespace and name to index a slice or make a Tekton client call, and a
// panic here has in the past taken the whole watcher down on every restart.
//
// Both segments are checked against the client's own path-segment rules. A
// segment the client refuses to encode (".", ".." or one containing "%") fails
// locally, before any request, with a plain error that is not a NotFound, so
// callers would treat it as retryable and InitQueues would abort startup on
// every restart over a single bad annotation.
func SplitPrKey(key string) (namespace, name string, ok bool) {
	namespace, name, found := strings.Cut(strings.TrimSpace(key), "/")
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if !found || namespace == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	if len(rest.IsValidPathSegmentName(namespace)) != 0 || len(rest.IsValidPathSegmentName(name)) != 0 {
		return "", "", false
	}
	return namespace, name, true
}
