// Package transform provides cache transform functions for reducing memory
// usage in the PAC watcher informer caches.
//
// Transform functions are applied to objects before they are stored in the
// informer cache, allowing us to strip large, unnecessary fields while
// preserving the data needed for reconciliation.
//
// DEVELOPER WARNING:
// If you add new reconciliation logic that reads a field from cached objects
// (via listers), you MUST verify that field is not stripped by these transforms.
// Fields stripped from cached objects will be nil/empty even though they exist
// in etcd. If you need a stripped field, fetch the full object via the API
// client instead of the lister.
package transform

import (
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/client-go/tools/cache"
)

// RepositoryForCache strips fields from Repository objects before they are
// stored in the informer cache to reduce memory usage.
//
// Fields stripped:
//   - ManagedFields: written by the API server, not needed for reconciliation
//   - Annotations: no reconciler logic reads Repository annotations from the
//     lister; the largest annotation is kubectl.kubernetes.io/last-applied-configuration
//   - Status: the reconciler always fetches Repository.Status via a direct API
//     call before updating it; it is never read from the lister
func RepositoryForCache(obj any) (any, error) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		transformed, err := RepositoryForCache(tombstone.Obj)
		if err != nil {
			return obj, nil //nolint:nilerr // return original on error for graceful degradation
		}
		return cache.DeletedFinalStateUnknown{Key: tombstone.Key, Obj: transformed}, nil
	}

	repo, ok := obj.(*pacv1alpha1.Repository)
	if !ok {
		return obj, nil
	}

	repo.ManagedFields = nil
	repo.Annotations = nil
	repo.Status = nil

	return repo, nil
}

// PipelineRunForCache strips fields from PipelineRun objects before they are
// stored in the informer cache to reduce memory usage.
//
// Fields the PAC watcher reads from cached PipelineRuns:
//   - ObjectMeta: name, namespace, labels, annotations (PAC state/repo keys),
//     finalizers, deletionTimestamp
//   - Spec.Status: checked for PipelineRunSpecStatusPending
//   - Status.Conditions: checked for completion state and reason
//   - Status.StartTime, Status.CompletionTime: used for metrics
//
// All other Spec and Status fields are stripped. When the reconciler needs
// the full object (e.g. postFinalStatus, GetStatusFromTaskStatusOrFromAsking),
// it fetches it directly from the API server.
func PipelineRunForCache(obj any) (any, error) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		transformed, err := PipelineRunForCache(tombstone.Obj)
		if err != nil {
			return obj, nil //nolint:nilerr // return original on error for graceful degradation
		}
		return cache.DeletedFinalStateUnknown{Key: tombstone.Key, Obj: transformed}, nil
	}

	pr, ok := obj.(*tektonv1.PipelineRun)
	if !ok {
		return obj, nil
	}

	pr.ManagedFields = nil

	// Strip large Spec fields — watcher only checks Spec.Status (pending state)
	pr.Spec.PipelineRef = nil
	pr.Spec.PipelineSpec = nil
	pr.Spec.Params = nil
	pr.Spec.Workspaces = nil
	pr.Spec.TaskRunSpecs = nil
	pr.Spec.TaskRunTemplate = tektonv1.PipelineTaskRunTemplate{}
	pr.Spec.Timeouts = nil

	// Strip large Status fields — watcher only reads Conditions, StartTime, CompletionTime
	pr.Status.PipelineSpec = nil
	pr.Status.ChildReferences = nil
	pr.Status.Provenance = nil
	pr.Status.SpanContext = nil

	return pr, nil
}
