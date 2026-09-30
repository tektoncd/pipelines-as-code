---
title: Concurrency
weight: 2
---

This page explains how to limit the number of concurrent PipelineRuns for a Repository CR and how to integrate with Kueue for Kubernetes-native job queueing. Use concurrency limits when you need to control resource consumption or prevent PipelineRuns from overwhelming your cluster.

Set the `concurrency_limit` field to define the maximum number of PipelineRuns running at any time for a Repository CR. This prevents resource exhaustion and ensures predictable scheduling when multiple events arrive in rapid succession.

```yaml
spec:
  concurrency_limit: <number>
```

When multiple PipelineRuns match the event, Pipelines-as-Code starts them in alphabetical order by PipelineRun name.

Example:

If you have three PipelineRuns in your `.tekton/` directory and you create a pull
request with a `concurrency_limit` of 1 in the repository configuration,
Pipelines-as-Code executes all PipelineRuns in alphabetical order, one after the
other. At any given time, only one PipelineRun is in the running state,
while the rest are queued.

For additional concurrency strategies and global configuration options, see [Advanced Concurrency]({{< relref "/docs/advanced/concurrency" >}}).

## Kueue - Kubernetes-native Job Queueing

If you need more sophisticated queue management than `concurrency_limit` provides, Pipelines-as-Code supports [Kueue](https://kueue.sigs.k8s.io/) as an alternative, Kubernetes-native solution for queuing PipelineRuns.
To get started, deploy the experimental integration provided by the [tektoncd/tekton-kueue](https://github.com/tektoncd/tekton-kueue) project. This allows you to schedule PipelineRuns through Kueue's queuing mechanism.

{{< callout type="info" >}}
The [tektoncd/tekton-kueue](https://github.com/tektoncd/tekton-kueue) project and the Pipelines-as-Code integration is only intended for testing.
It is only meant for experimentation and should not be used in production environments.
{{< /callout >}}
