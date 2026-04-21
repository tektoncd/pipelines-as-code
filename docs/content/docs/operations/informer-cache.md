---
title: Informer Cache Optimization
weight: 9
---

This page describes the informer cache transform functions that
Pipelines-as-Code uses to reduce the memory footprint of its watcher
controller. These transforms are applied automatically and require no
configuration.

## Background

The PAC watcher controller maintains in-memory caches (via Kubernetes
[informers](https://pkg.go.dev/k8s.io/client-go/tools/cache#SharedInformer))
for **Repository** and **PipelineRun** objects. In clusters with many
repositories or long-running PipelineRuns, these caches can consume a
significant amount of memory because each cached object carries fields that
the watcher never reads — `managedFields`, `last-applied-configuration`
annotations, embedded specs, and status details.

Pipelines-as-Code registers
[TransformFunc](https://pkg.go.dev/k8s.io/client-go/tools/cache#TransformFunc)
callbacks on each informer. A TransformFunc is called on every object
**before** it is stored in the cache, allowing unnecessary fields to be
stripped while the object is still in use.

This is the same approach used by the
[Tekton Pipelines controller](https://github.com/tektoncd/pipeline/pull/9316)
to reduce its own cache memory usage.

## What Gets Stripped

### Repository Objects

| Field | Why it is safe to strip |
| --- | --- |
| `metadata.managedFields` | Written by the API server for server-side apply tracking; not read by any reconciler logic |
| `metadata.annotations` | No reconciler logic reads Repository annotations from the lister; the largest annotation (`kubectl.kubernetes.io/last-applied-configuration`) can be 500-2000 bytes alone |
| `status` | The reconciler always fetches `Repository.Status` via a direct API call before updating it; it is never read from the lister |

**Benchmark result:** ~89% JSON size reduction per Repository object.

### PipelineRun Objects

#### Fields preserved (required for reconciliation)

| Field | Used for |
| --- | --- |
| `metadata.name`, `metadata.namespace` | Object identity |
| `metadata.labels` | Repository lookup, pipeline identification |
| `metadata.annotations` | PAC state (`pipelinesascode.tekton.dev/state`), repository keys |
| `metadata.finalizers`, `metadata.deletionTimestamp` | Finalizer-based cleanup |
| `spec.status` | Detecting `PipelineRunSpecStatusPending` |
| `status.conditions` | Completion state and reason |
| `status.startTime`, `status.completionTime` | Metrics recording |

#### Fields stripped

| Field | Why it is safe to strip |
| --- | --- |
| `metadata.managedFields` | API server bookkeeping, not used in reconciliation |
| `spec.pipelineRef` | Not read from the cache by the watcher |
| `spec.pipelineSpec` | Embedded pipeline definition; can be very large (~20KB in production) |
| `spec.params` | Not read from the cache |
| `spec.workspaces` | Not read from the cache |
| `spec.taskRunSpecs` | Not read from the cache |
| `spec.taskRunTemplate` | Not read from the cache |
| `spec.timeouts` | Not read from the cache |
| `status.pipelineSpec` | Snapshot of the executed pipeline spec; the largest status field (~20KB) |
| `status.childReferences` | References to child TaskRuns |
| `status.provenance` | Build provenance metadata |
| `status.spanContext` | Tracing span context |

When the reconciler needs the full PipelineRun (for example, during
`postFinalStatus` or `GetStatusFromTaskStatusOrFromAsking`), it fetches the
complete object directly from the API server.

**Benchmark result:** ~94% JSON size reduction per PipelineRun object.

## Memory Impact

Benchmarks using realistic object sizes from production clusters show the
following per-object savings:

| Object | Original size | After transform | Reduction |
| --- | --- | --- | --- |
| Repository (5 status entries) | ~5.7 KB | ~0.6 KB | ~89% |
| PipelineRun (15-task pipeline) | ~10.7 KB | ~0.7 KB | ~94% |

For a cluster with 1000 Repositories and 700 PipelineRuns, the estimated
watcher cache reduction is approximately **12 MB**.

## Graceful Degradation

The transform functions are designed to degrade gracefully:

- If an object is wrapped in a
  [`DeletedFinalStateUnknown`](https://pkg.go.dev/k8s.io/client-go/tools/cache#DeletedFinalStateUnknown)
  tombstone (which happens when the watcher misses a delete event), the
  transform unwraps it, strips the inner object, and re-wraps it.
- If the transform receives an unexpected type, it returns the object
  unmodified rather than returning an error.
- If any error occurs during transformation, the original object is returned
  unchanged so that the informer cache continues to function.

## Developer Notes

If you add new reconciliation logic that reads a field from cached objects
(via listers), you **must** verify that the field is not stripped by these
transforms. Fields stripped from cached objects will be `nil` or empty even
though they exist in etcd.

If you need a stripped field, fetch the full object via the API client
instead of the lister:

```go
// Don't do this — spec.params is stripped from the cache:
pr, _ := pipelineRunLister.PipelineRuns(ns).Get(name)
params := pr.Spec.Params // always nil!

// Do this instead — fetch from the API server:
pr, _ := tektonClient.TektonV1().PipelineRuns(ns).Get(ctx, name, metav1.GetOptions{})
params := pr.Spec.Params // full object from etcd
```

The transform functions and their benchmarks live in
[`pkg/informer/transform/`](https://github.com/tektoncd/pipelines-as-code/tree/main/pkg/informer/transform).

To run the benchmarks yourself:

```bash
go test -bench=. -benchmem -v ./pkg/informer/transform/
```

To see the size reduction report:

```bash
go test -v -run 'TestMeasure.*TransformSavings' ./pkg/informer/transform/
```
