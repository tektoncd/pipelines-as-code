package queue

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
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
}

func RepoKey(repo *v1alpha1.Repository) string {
	return fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
}

func PrKey(run *tektonv1.PipelineRun) string {
	return fmt.Sprintf("%s/%s", run.Namespace, run.Name)
}

// SplitPrKey parses a "namespace/name" queue key, the inverse of PrKey.
//
// The key can come straight from a user-editable annotation (execution-order)
// or from an internal queue that in principle only ever stores what PrKey
// produced, but every caller needs the same defensive parsing: a malformed
// entry must be rejected, not indexed, since callers use namespace/name to
// index directly into a slice or make a Tekton client call, and a panic here
// has in the past taken down the whole watcher on every restart.
func SplitPrKey(key string) (namespace, name string, ok bool) {
	namespace, name, found := strings.Cut(strings.TrimSpace(key), "/")
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if !found || namespace == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return namespace, name, true
}
