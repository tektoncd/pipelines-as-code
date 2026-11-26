---
title: PipelineRun Follow-ups
weight: 3
---

A follow-up is a PipelineRun that runs after other PipelineRuns of the same event have finished, if a condition you write holds. Use it to deploy once the build and the tests succeeded, to analyze a failed build, or to pass results from one run to the next.

{{< tech_preview "PipelineRun follow-ups" >}}

Follow-ups are disabled by default. Set `enable-follow-up-runs` to `"true"` in the [Pipelines-as-Code ConfigMap]({{< relref "/docs/api/configmap" >}}) to use them.

## Declaring a follow-up

A follow-up is a PipelineRun definition in `.tekton/` with two annotations:

- `pipelinesascode.tekton.dev/on-pipelinerun`: a JSON array of the definitions it waits on.
- `pipelinesascode.tekton.dev/on-cel-expression`: the condition. The follow-up runs only when it is true, once all the definitions it waits on have finished.

Here `deploy` runs only when `build` and `tests` both succeeded:

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

```mermaid
flowchart LR
%% title: How a follow-up waits on its dependencies and decides to run
%% autoplay
  Ev@{ img: "/icons/webhook.svg", label: "Event", w: 48, h: 48, constraint: "on" }
  Ctrl@{ img: "/icons/controller.svg", label: "Controller", w: 48, h: 48, constraint: "on" }
  Build@{ img: "/icons/build.svg", label: "build", w: 48, h: 48, constraint: "on" }
  Tests@{ img: "/icons/test.svg", label: "tests", w: 48, h: 48, constraint: "on" }
  Watch@{ img: "/icons/monitor.svg", label: "Watcher", w: 48, h: 48, constraint: "on" }
  Cond@{ img: "/icons/decision.svg", label: "Condition", w: 48, h: 48, constraint: "on" }
  Deploy@{ img: "/icons/deploy.svg", label: "deploy", w: 48, h: 48, constraint: "on" }
  Drop@{ img: "/icons/stop.svg", label: "Not run", w: 48, h: 48, constraint: "on" }
  Git@{ img: "/icons/pull-request.svg", label: "Pull request", w: 48, h: 48, constraint: "on" }
  Ev --> Ctrl
  Ctrl --> Build
  Ctrl --> Tests
  Ctrl skip@-->|skipped| Git
  Build --> Watch
  Tests --> Watch
  Watch --> Cond
  Cond yes@-->|true| Deploy
  Cond no@-->|false| Drop
  Deploy --> Git

%% play: Both succeed
%% skip Drop
%% Ev -> Ctrl : PR #42
%% Ctrl -> Build, Tests | build and tests start, deploy is stored on them and waits
%% Build, Tests -> Watch : Succeeded
%% wait 1200 | The watcher evaluates the follow-up only once every run it waits on has finished
%% Watch -> Cond : runs | The definition is read again at the commit of the event and its condition evaluated
%% Cond -> Deploy @yes : digest | Results of build and tests are passed through spec.params
%% Deploy -> Git : status
%% play: Condition false
%% skip Deploy
%% Ev -> Ctrl : PR #43
%% Ctrl -> Build, Tests
%% Build, Tests -> Watch : Failed
%% Watch -> Cond : runs
%% Cond -> Drop @no | The condition is false, so nothing runs and nothing is reported
%% play: Dependency did not match
%% skip Tests Watch Cond Deploy Drop
%% Ev -> Ctrl : PR #44
%% Ctrl -> Build | Only build matches this event, tests targets another branch
%% Ctrl -> Git @skip : skipped | The follow-up can never have all its dependencies, so it is reported as skipped right away
```

A few rules:

- Use the `metadata.name` of a definition to refer to it, or its `generateName` with the trailing dash, for example `"build-"`.
- A follow-up can wait on regular definitions only, not on another follow-up.
- A follow-up name must be lowercase and at most 52 characters.
- Only `on-cel-expression` decides whether a follow-up matches. `on-event`, `on-target-branch` and the other matching annotations are ignored.
- All the definitions of a group must use the same `target-namespace`, or none.

An invalid follow-up, for example one waiting on an undefined definition or on another follow-up, rejects the event and none of its PipelineRuns start.

## Writing the condition

The condition has a `runs` entry per definition it waits on:

| Field | Description |
| --- | --- |
| `runs.<name>.status` | `Succeeded`, `Failed`, `Cancelled`, or `Error` (the run was pruned or never created). |
| `runs.<name>.reason` | The Tekton condition reason, for example `PipelineRunTimeout`. |
| `runs.<name>.name` | The name of the PipelineRun. |
| `runs.<name>.results` | The Pipeline results of the run, as strings. |

A failed dependency does not fail the follow-up, the condition decides. Use `runs.build.status == "Failed"` to run a follow-up that analyzes failed builds.

For a name containing `-` or `.`, use the index syntax: `runs["unit-tests"].status == "Succeeded"`.

The condition can also use `event`, `event_type`, `target_branch`, `source_branch` and the other [CEL variables]({{< relref "/docs/guides/event-matching/cel-expressions" >}}) of the event, except those based on the webhook payload: `body`, `headers`, `files` and `pathChanged()` are not available. To decide on the payload or the changed files, put a CEL expression on the definitions the follow-up waits on.

The follow-up is created later, without the original event. `{{ body.* }}`, `{{ headers.* }}` and `{{ files.* }}` are rejected, but other values that come from the triggering event are not available either and resolve to empty values without an error: `{{ trigger_comment }}` and parameters passed in a comment such as `/test key=value`.

## Passing results

Use `{{ runs.<name>.results.<result> }}` in `spec.params` to pass a result of a dependency to the follow-up:

```yaml
spec:
  params:
    - name: image
      value: "registry.example.com/app@{{ runs.build.results.digest }}"
```

Only `spec.params` is substituted. A placeholder anywhere else, such as a pipeline parameter default, a task parameter, a script or an environment variable, is left as is and fails when the run executes.

A run exposes at most 32 results, and values larger than 4096 bytes are dropped. A parameter referencing a missing result skips the follow-up.

## Passing run identity

`runs.<name>.uid` contains the Kubernetes UID, empty for a missing dependency.

The `name`, `uid`, `status` and `reason` fields can also be passed in `spec.params`, for example `{{ runs.build.uid }}`. A missing UID is unresolved and skips the follow-up; it cannot silently select a replacement run with the same name.

## When a follow-up does not run

- The condition is false: nothing runs and nothing is reported.
- Some of the definitions it waits on do not match the event, for example because `tests` targets another branch: the follow-up is reported as skipped.
- None of them matches the event: the follow-up does not match either, and nothing is reported.
- A run it waits on is deleted while it waits: the follow-up is skipped without evaluating its condition if the run is the first dependency by name, for example `build` for `build` and `tests`. Otherwise the condition sees that dependency as `Error`.

## Re-running

- `/retest` re-runs the failed definitions. A follow-up is re-created only if all the definitions it waits on run again.
- `/test` without a name re-runs every definition and all the follow-ups.
- `/test <name>` on a follow-up is rejected: re-run one of its dependencies instead.

A follow-up always sees the results of the latest runs of its dependencies.

## Diagnostic comments

GitHub comments starting with the reserved `<!-- paco-diagnosis source=` marker are diagnostic output. They are ignored before GitOps command detection and custom comment matching, including when edited, so evidence quoted in a diagnosis cannot trigger another run.

## Concurrency and cancellation

A follow-up counts against the Repository [concurrency limit]({{< relref "/docs/guides/repository-crd/concurrency" >}}) like any other PipelineRun. `/cancel` and [cancel-in-progress]({{< relref "/docs/guides/running-pipelines#cancelling-in-progress-pipelineruns" >}}) cancel the follow-ups of the event, waiting ones included. Closing the pull request does so only when cancel-in-progress applies to the definitions the follow-up waits on, through `enable-cancel-in-progress-on-pull-requests` (off by default) or the annotation.

A cancelled dependency can be reported as finished while Tekton is still stopping it. A follow-up whose condition ignores the status of its dependencies, such as `true`, may then start before the dependency has fully stopped. Test the status in the condition to avoid this, for example `runs.build.status == "Succeeded"`.
