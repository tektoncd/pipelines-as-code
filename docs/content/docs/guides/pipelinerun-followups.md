---
title: PipelineRun Follow-ups
weight: 3
---

This page explains how to run a PipelineRun only after other PipelineRuns of the same event have finished, and how to pass their results along. Use follow-ups when a PipelineRun depends on the outcome of others, for example to deploy only when the build and the tests succeeded, or to analyze a failed build.

{{< tech_preview "PipelineRun follow-ups" >}}

## Declaring a follow-up

A follow-up is a PipelineRun definition in your `.tekton/` directory that carries the `pipelinesascode.tekton.dev/on-pipelinerun` annotation. The annotation is a JSON array of the definitions the follow-up waits on, its dependencies. Every follow-up must also carry a `pipelinesascode.tekton.dev/on-cel-expression` annotation: Pipelines-as-Code evaluates it once all the dependencies have finished, and creates the follow-up only when it is true.

The following `.tekton/` directory runs `build` and `tests` on every pull request targeting `main`, then runs `deploy` only when both succeeded:

```yaml
---
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: build
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
---
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: tests
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
---
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build", "tests"]'
    pipelinesascode.tekton.dev/on-cel-expression: |
      runs.build.status == "Succeeded" && runs.tests.status == "Succeeded"
```

Dependencies are listed by their definition name: the `metadata.name` of the definition or, for a definition that uses `metadata.generateName`, the `generateName` value including its trailing dash. A definition declared with `generateName: build-` is listed as `"build-"`.

A follow-up can wait on another follow-up: it waits until that one has itself been created and finished.

{{< callout type="info" >}}
A follow-up matches on its `on-cel-expression` only. The `on-event`, `on-target-branch`, `on-comment`, `on-label`, `on-path-change` and `on-path-change-ignore` annotations have no effect on it: Pipelines-as-Code plans a follow-up for an event when its dependencies run for that event.
{{< /callout >}}

## How a follow-up runs

When an event matches, Pipelines-as-Code creates the dependencies as usual and records the follow-up on their runs. The follow-up has no footprint while it waits: it is not a PipelineRun object, it posts no status check, holds no concurrency slot, and needs no cancelling in the cluster.

Once every dependency has finished, Pipelines-as-Code reads the follow-up definition again from the repository, at the revision the event pinned, and evaluates its CEL condition. When the condition is true, Pipelines-as-Code creates the follow-up run, once. When it is false, nothing is reported: like a definition whose `on-cel-expression` does not match the event, the follow-up does not run. The first status check of a follow-up appears when its run is created.

The follow-up run gets a deterministic name, `<definition>-<group id>`, where the group id identifies the runs of the event and is 10 characters long. Because a PipelineRun name is at most 63 characters, a follow-up definition name is limited to 52 characters, a `generateName` trailing dash included. Pipelines-as-Code rejects a longer name when it processes the event.

## Writing the follow-up condition

The follow-up condition sees one `runs` entry per dependency, keyed by definition name:

| Field | Description |
| --- | --- |
| `runs.<name>.name` | The name of the created PipelineRun. |
| `runs.<name>.status` | `Succeeded`, `Failed`, `Cancelled`, or `Error`. `Error` means the run could not be found: it was pruned or never created. |
| `runs.<name>.reason` | The Tekton condition reason, kept as detail, for example `PipelineRunTimeout`. |
| `runs.<name>.results` | The Pipeline results of the run, as strings. Array and object results are JSON encoded. |

A definition name that contains a `-` or a `.` is not a valid CEL field name. Use the index syntax instead, for example `runs["unit-tests"].status == "Succeeded"` or `runs["build-"].status == "Succeeded"` for a `generateName` definition.

Apart from `runs`, Pipelines-as-Code evaluates the condition exactly like an [`on-cel-expression`]({{< relref "/docs/guides/event-matching/cel-expressions" >}}) of the event:

- It sees the `event`, `event_type`, `event_title`, `target_branch`, `source_branch`, `target_url` and `source_url` values the event was matched with, including the `refs/heads/` handling of push branches.
- It sees the Repository [custom parameters]({{< relref "/docs/advanced/custom-parameters" >}}) as their `filter` selected them when Pipelines-as-Code processed the event, even a filter that reads the payload. Pipelines-as-Code records only which filters matched, never the parameter values: it reads the values again, from their Secret for `secret_ref`, when it creates the follow-up.

{{< callout type="warning" >}}
The webhook payload is gone by the time Pipelines-as-Code evaluates a follow-up, so the follow-up condition cannot use `body`, `headers`, `files`, or the `pathChanged()` function, and the follow-up spec cannot use the `{{ body.* }}`, `{{ headers.* }}` or `{{ files.* }}` template variables. Pipelines-as-Code rejects them when it processes the event. To decide on the payload or on the changed files, put a [CEL expression]({{< relref "/docs/guides/event-matching/cel-expressions" >}}) on the dependencies instead.
{{< /callout >}}

Like any `on-cel-expression`, the condition only has to compile: making it reachable is your responsibility, and Pipelines-as-Code does not report a false one. A dependency that finished with a non-success status does not fail the follow-up by itself; the condition decides. Checking `runs.<name>.status == "Succeeded"` for every dependency is the usual all-green condition.

## Passing results to the follow-up

A follow-up can reference the results of its dependencies in `spec.params` with `{{ runs.<name>.<field> }}` placeholders:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
    pipelinesascode.tekton.dev/on-cel-expression: |
      runs.build.status == "Succeeded"
spec:
  params:
    - name: image
      value: "registry.example.com/app@{{ runs.build.results.digest }}"
```

The placeholders take the definition name as is, dots and dashes included: `{{ runs.build.v2.results.digest }}` reads the `digest` result of the `build.v2` definition.

Pipelines-as-Code resolves the placeholders when it creates the follow-up run, structurally in the created object. A result is plain data: a pull request author controlling the `build` PipelineRun cannot use a result value to inject another `{{ ... }}` reference or otherwise influence the templating of the follow-up. Placeholders outside `spec.params` are left as they are.

## What the follow-up sees at creation

Pipelines-as-Code reads the follow-up definition again when it creates the run, at the revision the event pinned, and templates it with the same variables as the event, its comment, labels and custom parameters included:

- In-repository content is exactly what the event saw.
- Remote tasks are fetched again and reflect what is current at creation time. Only the follow-up's own remote tasks are resolved.
- With `pipelinerun_provenance: default_branch`, the `.tekton` directory is read from the default branch as it is when the follow-up is created.

## Skipped and failed follow-ups

- **A dependency did not match the event.** When some dependencies run but another does not match the event, for example because `tests` is scoped to another branch, Pipelines-as-Code skips the follow-up when it processes the event and posts a skipped status check that names the dependency that did not run. A follow-up waiting on a skipped follow-up is skipped too. When none of the dependencies runs, the follow-up is not matched, like any definition that does not match the event.
- **The condition is false.** The follow-up does not run and Pipelines-as-Code reports nothing.
- **The definition is gone.** When the follow-up definition is no longer found at the pinned revision, Pipelines-as-Code skips the follow-up with a status check that explains why.
- **A dependency was pruned.** A dependency removed from the cluster while the follow-up waits surfaces with the `Error` status, and the condition decides. Pipelines-as-Code delays the [cleanup]({{< relref "/docs/advanced/cleanup" >}}) of a completed run while a follow-up still waits on it.
- **A dependency is stuck.** Tekton handles it, for example with [`timeouts`](https://tekton.dev/docs/pipelines/pipelineruns/#configuring-pipelinerun-timeouts): the timeout fails the run, which creates or skips the follow-up.
- **The run cannot be created.** Pipelines-as-Code handles it like any PipelineRun that cannot start. It retries Git provider API calls only as configured by `enable-api-retry`. A failure that remains, such as a `.tekton` directory or remote task that cannot be fetched, or a PipelineRun the cluster rejects, posts a failed `pipelinerun start failure` status check with the error. Pipelines-as-Code does not retry the follow-up; a `/test` re-runs the whole group.

Pipelines-as-Code rejects the event and starts none of its PipelineRuns when the follow-ups are invalid: a definition that depends on itself, a dependency loop, a dependency that is not defined in the `.tekton/` directory, a missing `on-cel-expression` annotation, an expression that does not compile, or a follow-up whose [`target-namespace`]({{< relref "/docs/guides/repository-crd#setting-pipelinerun-definition-source" >}}) annotation resolves to another Repository than its dependencies. A follow-up runs next to its dependencies, with their Repository: give the whole group the same `target-namespace`, or none.

## Re-running a group

- `/retest` and `/ok-to-test` re-run the **failed** definitions of the event. Follow-ups whose dependencies all run again are re-created and wait as usual. A follow-up with a dependency that already succeeded is not re-created, because the group cannot re-form around a mix of new and past runs; the status checks of its previous run, if it had one, stay on the commit.
- `/test` without a name re-runs **every** definition of the event and re-creates the whole group of follow-ups. Use it to re-run a group, for example after a follow-up itself failed.
- `/test <name>` and `/retest <name>` targeting a follow-up are rejected with a comment: a follow-up only makes sense inside its group, and running it alone would leave its `{{ runs.* }}` placeholders unresolved. Targeting the other definitions of the event works as usual.
- An [incoming webhook]({{< relref "/docs/advanced/incoming-webhooks" >}}) that targets a follow-up is rejected the same way: no run is created and a Repository event explains why. Target one of its dependencies instead; the follow-up then runs after it as on any other event. An incoming webhook that overrides `params` is rejected when follow-ups would run for it: Pipelines-as-Code does not store the values, so the follow-ups would not see them. Set those params in the Repository [custom parameters]({{< relref "/docs/advanced/custom-parameters" >}}) instead.

You cannot re-run a follow-up alone against the results of the previous runs of its dependencies: when the group re-runs, the follow-up sees the results of the **new** dependency runs. Take a failure analysis follow-up:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: analysis
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
    pipelinesascode.tekton.dev/on-cel-expression: |
      runs.build.status == "Failed"
```

The analysis runs whenever the build fails. If the analysis itself fails, a `/test` re-runs the build, which fails again since the code has not changed, and then the analysis on that new run. Pushing a new commit runs the whole chain the same way.

## Concurrency and cancellation

A created follow-up counts against the Repository [concurrency limit]({{< relref "/docs/guides/repository-crd/concurrency" >}}) and joins the queue like any other PipelineRun.

`/cancel`, closing a pull request, and [cancel-in-progress]({{< relref "/docs/guides/running-pipelines#cancelling-in-progress-pipelineruns" >}}) also reach waiting follow-ups, so a follow-up never runs against a superseded event: Pipelines-as-Code posts a cancelled status check for it and drops it, so no run is created for it later. A cancelled dependency surfaces as `runs.<name>.status == "Cancelled"`, and the condition of the follow-up decides.

When cancel-in-progress supersedes a run, it also cancels the follow-up runs already created after it, directly or through a chain, even when the superseded run itself has finished, and the follow-ups waiting on them. Follow-ups of the superseded event that do not depend on the superseded run are left alone, like the runs of the other definitions.

{{< callout type="info" >}}
A waiting follow-up is stored on one of its dependency runs, the first of its root dependencies by name. When a newer event supersedes only another of its dependencies, the waiting follow-up is not cancelled: it is still created once its dependencies finish, with the results of the superseded run. For example, a `deploy` waiting on `build` and `tests` is stored on `build`, and a newer event that re-runs only `tests` does not cancel it. To avoid it, make the dependencies of a follow-up match the same events, so a newer event supersedes them all.
{{< /callout >}}
