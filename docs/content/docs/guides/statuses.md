---
title: PipelineRun status
weight: 7
---

This page describes how Pipelines-as-Code reports PipelineRun statuses across different Git providers, including log snippets, error annotations, and notification options.

A GitHub App reports through check runs, a webhook through commit statuses and pull request comments:

```mermaid
flowchart LR
%% title: How a PipelineRun status reaches the Git provider
%% autoplay
  Ctrl@{ img: "/icons/controller.svg", label: "Controller", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "PipelineRun", w: 48, h: 48, constraint: "on" }
  Watch@{ img: "/icons/monitor.svg", label: "Watcher", w: 48, h: 48, constraint: "on" }
  Logs@{ img: "/icons/logs.svg", label: "Log snippet", w: 48, h: 48, constraint: "on" }
  Mask@{ img: "/icons/secret.svg", label: "Hide secrets", w: 48, h: 48, constraint: "on" }
  Report[Task breakdown]
  Check@{ img: "/icons/check.svg", label: "Check run", w: 48, h: 48, constraint: "on" }
  Status@{ img: "/icons/commit.svg", label: "Commit status", w: 48, h: 48, constraint: "on" }
  Comment@{ img: "/icons/comment.svg", label: "PR comment", w: 48, h: 48, constraint: "on" }
  Ctrl --> Run
  Ctrl app@-->|GitHub App| Check
  Ctrl hook@-->|webhook| Status
  Run --> Watch
  Watch ok@-->|succeeded| Report
  Watch bad@-->|failed| Logs
  Logs --> Mask --> Report
  Report --> Check
  Report --> Status
  Report -->|pull request| Comment

%% play: GitHub App, failed task
%% skip Status Comment
%% Ctrl -> Run
%% Ctrl -> Check @app : in progress | The controller creates a check run as soon as the PipelineRun starts
%% Run -> Watch : done
%% Watch -> Logs @bad : failed | The last lines of the first failed task are collected
%% Logs -> Mask : 3 lines | Secret values from the environment of tasks and steps become *****
%% Mask -> Report
%% Report -> Check : failure | The check run is updated with the breakdown and the snippet
%% play: Webhook, pull request
%% skip Check Logs Mask
%% Ctrl -> Run
%% Ctrl -> Status @hook : pending | Without a GitHub App, a commit status is set instead
%% Run -> Watch : done
%% Watch -> Report @ok : succeeded
%% Report -> Status : success
%% Report -> Comment | On a pull request, the breakdown is posted as a comment
%% play: Webhook, push
%% skip Check Logs Mask Comment
%% Ctrl -> Run
%% Ctrl -> Status @hook : pending
%% Run -> Watch : done
%% Watch -> Report @ok : succeeded
%% Report -> Status : success | A push has no comment, only the commit status
```

## GitHub Apps

After a PipelineRun finishes, Pipelines-as-Code displays its status
in the GitHub Check tabs with a concise overview
of the status, name, and duration of each task in the pipeline. If a task has a
[displayName](https://tekton.dev/docs/pipelines/tasks/#specifying-a-display-name),
Pipelines-as-Code uses it as the task description; otherwise it uses the task
name.

If any step fails, Pipelines-as-Code includes a small portion of the log from that step
in the output.

## Step breakdown

Under every task of the breakdown, Pipelines-as-Code lists the steps that ran,
with their status, their duration and a link to their own logs on the console.
This is useful when a task is assembled out of several
[StepActions](https://tekton.dev/docs/pipelines/stepactions/), since the task
name alone does not say much about what ran.

A step shows its
[displayName](https://tekton.dev/docs/pipelines/tasks/#specifying-a-display-name)
when it has one, otherwise its name without any leading `step-`. Steps that
never ran because an earlier step of the same task failed are left out, so a
failing task shows the step that broke. A task that only has one step keeps
showing a single line.

On GitHub, GitLab and Gitea the task name becomes a collapsible block holding
the steps, so the table keeps one line per task until you unfold it. The
Bitbucket providers list the steps as extra lines because their comments cannot
fold content.

The step links point to the logs of that step on the OpenShift Console or the
Tekton Dashboard. For a custom console, set `custom-console-url-pr-steplog` with
a `{{ step }}` variable, otherwise the links fall back to the task logs.

Set `status-show-steps` to `false` in the Pipelines-as-Code ConfigMap to report
only the tasks.

If an error occurs while creating the PipelineRun on the cluster,
Pipelines-as-Code surfaces the error message from the Pipeline Controller in the
GitHub user interface. This helps you identify and
troubleshoot the issue without navigating to the underlying
infrastructure.

Pipelines-as-Code also reports any other errors during pipeline execution
in the GitHub user interface. However, if no namespace matches,
Pipelines-as-Code logs the error in its controller logs instead.

## Statuses for other providers (webhook-based)

For webhook events related to a pull request, Pipelines-as-Code posts a
comment on the corresponding pull or merge request. For
push events, there is no dedicated space to display the PipelineRun status. In those cases, use the
alternate methods described below.

## Log Snippet when reporting errors

When Pipelines-as-Code detects an error in the pipeline tasks, it displays a brief excerpt of
the last three lines of each failed task, in the order the tasks finished. The
GitHub check run output is limited to 65,535 characters, so when the snippets
do not all fit, Pipelines-as-Code shows as many as fit and ends with a line
saying how many failed tasks it left out.

To prevent exposing secrets, Pipelines-as-Code analyzes the PipelineRun and
replaces secret values with hidden characters. It retrieves
all secrets from the environment variables associated with tasks and steps, then
searches for matches of those values in the output snippet.

Pipelines-as-Code sorts matches by length and replaces them with a
`"*****"` placeholder in the output snippet. This ensures that the output
does not contain any leaked secrets.

Secret hiding does not support concealing secrets from `workspaces`
or
[envFrom](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.23/#envfromsource-v1-core)
sources.

![log snippet](/images/snippet-failure-message.png)

### Error detection from containers logs as GitHub Annotation

If you enable the `error-detection-from-container-logs` option in the
Pipelines-as-Code ConfigMap, Pipelines-as-Code attempts to detect
errors from the container logs and add them as annotations on the corresponding
pull request where the error occurred.

Currently, Pipelines-as-Code only supports a simple error format resembling `makefile` or
`grep` output, specifically the format:

```console
filename:line:column: error message
```

Tools like `golangci-lint`, `pylint`, `yamllint`, and many others can
produce errors in this format.

Refer to the Pipelines-as-Code
[pull_request.yaml](https://github.com/tektoncd/pipelines-as-code/blob/7c9b16409a1a6c93e9480758f069f881e5a50f05/.tekton/pull-request.yaml#L70)
for an example of linting code and outputting errors in the
specified format.

You can customize the regular expression used for detecting errors with the
`error-detection-simple-regexp` setting. The regular expression uses [named
groups](https://www.regular-expressions.info/named.html) to provide flexibility
in specifying the matching criteria. The necessary groups for matching are
filename, line, and error (the column group is not used). The default regular
expression is defined in the configuration map.

By default, Pipelines-as-Code searches for errors in only the last 50 lines of
the container logs. You can increase this limit by setting the
`error-detection-max-number-of-lines` value. If you set this value to -1, the
system searches through all available lines for errors. Be aware that
increasing this maximum number of lines may increase the memory usage of the
watcher.

![annotations](/images/github-annotation-error-failure-detection.png)

## Namespace Event Stream

When a namespace matches a repository, Pipelines-as-Code emits
log messages as Kubernetes events in the namespace of the corresponding
Repository CR.

## Repository CR

```console
% kubectl get repo -n pipelines-as-code-ci
NAME                  URL                                                        NAMESPACE             SUCCEEDED   REASON      STARTTIME   COMPLETIONTIME
pipelines-as-code-ci   https://github.com/tektoncd/pipelines-as-code   pipelines-as-code-ci   True        Succeeded   59m         56m
```

You can use the `tkn pac describe` command from the [CLI]({{< relref "/docs/cli/" >}}) to view
all statuses of PipelineRuns associated with your repository and
their metadata.

## Status checks for unmatched PipelineRuns

By default, Pipelines-as-Code only reports status for PipelineRuns that matched and ran. If you want to see which PipelineRuns were skipped because their annotations did not match the incoming event, enable the `status_checks` setting in your Repository CR. See the [Status Checks guide]({{< relref "/docs/guides/repository-crd/status-checks" >}}) for configuration and provider behavior.

## Notifications

Pipelines-as-Code does not manage notifications directly. Instead, you can add notifications to your PipelineRuns using the [finally feature of
Tekton
Pipelines](https://github.com/tektoncd/pipeline/blob/main/docs/pipelines.md#adding-finally-to-the-pipeline).
This feature executes a set of tasks at the end of a
PipelineRun, regardless of whether it succeeds or fails.

For an example, see the [release generation PipelineRun](https://github.com/tektoncd/pipelines-as-code/blob/6dfcd60091c46844449e91a38bf38b76bea56c47/.tekton/generate-release.yaml#L165) in the
`.tekton/` directory of the Pipelines-as-Code repository. It uses the [finally
task with the guard
feature](https://tekton.dev/docs/pipelines/pipelines/#guard-finally-task-execution-using-when-expressions)
to send a notification to Slack when any failure occurs in the PipelineRun. See
it in action here:

<https://github.com/tektoncd/pipelines-as-code/blob/6dfcd60091c46844449e91a38bf38b76bea56c47/.tekton/generate-release.yaml#L174>
