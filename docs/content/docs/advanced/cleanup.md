---
title: PipelineRuns Cleanup
weight: 1
---
This page explains how to automatically remove old PipelineRuns so that completed runs do not accumulate and consume cluster resources. Use this when you want to retain only a fixed number of recent runs per PipelineRun definition.

## Configuring cleanup

To limit the number of retained runs, add the following annotation to your PipelineRun:

```yaml
pipelinesascode.tekton.dev/max-keep-runs: "maxNumber"
```

After a PipelineRun finishes, whether it succeeded or failed, Pipelines-as-Code detects this annotation and removes older PipelineRuns, keeping only the last `maxNumber` runs.

Pipelines-as-Code skips any PipelineRun in a `Running` or `Pending` state during cleanup. However, it does not skip PipelineRuns with an `Unknown` status.

```mermaid
flowchart LR
%% title: How max-keep-runs prunes older PipelineRuns
  Run@{ img: "/icons/pipeline.svg", label: "Finished run", w: 48, h: 48, constraint: "on" }
  Watch@{ img: "/icons/monitor.svg", label: "Watcher", w: 48, h: 48, constraint: "on" }
  Limit@{ img: "/icons/config.svg", label: "max-keep-runs", w: 48, h: 48, constraint: "on" }
  List@{ img: "/icons/search.svg", label: "Same definition", w: 48, h: 48, constraint: "on" }
  Pick@{ img: "/icons/decision.svg", label: "Keep?", w: 48, h: 48, constraint: "on" }
  Keep@{ img: "/icons/success.svg", label: "Kept", w: 48, h: 48, constraint: "on" }
  Del@{ img: "/icons/stop.svg", label: "Deleted", w: 48, h: 48, constraint: "on" }
  Sec@{ img: "/icons/secret.svg", label: "Git auth secret", w: 48, h: 48, constraint: "on" }
  Run --> Watch --> Limit --> List --> Pick
  Pick new@-->|newest| Keep
  Pick busy@-->|running| Keep
  Pick need@-->|follow-up| Keep
  Pick old@-->|older| Del
  Del --> Sec

%% play: Prune old runs
%% Run -> Watch : done | Cleanup runs after the final status is reported
%% Watch -> Limit : 3 | The annotation value, capped by max-keep-run-upper-limit, or default-max-keep-runs when there is no annotation
%% Limit -> List | Completed runs of the same PipelineRun definition in the Repository, newest first
%% List -> Pick
%% Pick -> Keep @new : 3 runs
%% Pick -> Del @old : older | Every completed run past the limit is deleted
%% Del -> Sec | Its leftover git auth secret is deleted too
%% play: Runs that stay
%% skip Del Sec
%% Run -> Watch : done
%% Watch -> Limit : 3
%% Limit -> List
%% List -> Pick
%% Pick -> Keep @busy | A run still Running or Pending is skipped
%% Pick -> Keep @need | So is an older run that a pending follow-up still waits on
```

{{< callout type="info" >}}
The cleanup limit can also be set globally for all repositories using the
[`default-max-keep-runs`]({{< relref "/docs/api/configmap#param-default-max-keep-runs" >}})
ConfigMap field. The
[`max-keep-run-upper-limit`]({{< relref "/docs/api/configmap#param-max-keep-run-upper-limit" >}})
field caps the maximum value a user may specify in the annotation.
{{< /callout >}}
