---
title: Advanced Commands
weight: 2
---

This page covers advanced GitOps command features. Use these capabilities when you need to go beyond basic `/test` and `/retest` commands -- for example, reading the triggering comment, defining custom commands, cancelling runs, or passing parameters.

## Accessing the Comment Triggering the PipelineRun

**What it does:** When you trigger a PipelineRun via a GitOps command, Pipelines-as-Code sets the template variable `{{ trigger_comment }}` to the full text of the comment that triggered it. You can then use this value in shell scripts or other steps to make decisions based on the comment content.

**Important:** Expanding `{{ trigger_comment }}` directly in YAML can break parsing if the comment contains newlines. For example, a GitHub comment like:

```console
/help

This is a test.
```

Expands to:

```yaml
params:
  - name: trigger_comment
    value: "/help

This is a test."
```

The empty line makes the YAML invalid. To prevent this, Pipelines-as-Code replaces `\r\n` with `\n` to ensure proper formatting. You can restore the newlines in your task as needed.

For example, in a shell script, use `echo -e` to expand `\n` back into actual newlines:

```shell
echo -e "{{ trigger_comment }}" > /tmp/comment
grep "/help" /tmp/comment # will output only /help
```

This ensures the comment is correctly formatted when processed.

## Custom GitOps Commands

**What it does:** The [on-comment]({{< relref "/docs/guides/event-matching/comment-and-label" >}}) annotation lets you define your own GitOps commands that trigger specific PipelineRuns when someone comments on a pull request.

**When to use it:** You want to go beyond the built-in `/test`, `/retest`, and `/cancel` commands and create domain-specific commands for your workflow. See the [on-comment]({{< relref "/docs/guides/event-matching/comment-and-label" >}}) guide for full details.

For a practical example, see the [pac-boussole](https://github.com/openshift-pipelines/pac-boussole) project, which uses the `on-comment` annotation to create a PipelineRun experience similar to [Prow](https://docs.prow.k8s.io/).

## GitOps Command Prefix

{{< support_matrix github_app="true" github_webhook="true" forgejo="false" gitlab="false" bitbucket_cloud="false" bitbucket_datacenter="false" >}}

You can configure a custom prefix for GitOps commands in the Repository CR. This allows you to use commands like `/pac test` instead of the standard `/test`. This is useful when both [prow](https://docs.prow.k8s.io/) CI and Pipelines-as-Code are configured on a Repository and making comments causes issues and confusion.

Please note that custom GitOps commands are excluded from this prefix settings.

To configure a custom GitOps command prefix, set the `gitops_command_prefix` field in your Repository CR's `settings` section:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: my-repository
  namespace: pipelines-as-code
spec:
  url: "https://github.com/organization/repository"
  settings:
    gitops_command_prefix: "pac"
```

Note: Set the prefix as a plain word (e.g. `pac`). The Forward slash (`/`) is added automatically by Pipelines-as-Code.

With this configuration, you can use the following prefixed commands:

- `/pac test` - Trigger all matching PipelineRuns
- `/pac test <pipelinerun-name>` - Trigger a specific PipelineRun
- `/pac retest` - Retest failed PipelineRuns
- `/pac retest <pipelinerun-name>` - Retest specific PipelineRun
- `/pac cancel` - Cancel all running PipelineRuns
- `/pac cancel <pipelinerun-name>` - Cancel Specific PipelineRun
- `/pac ok-to-test` - Approve CI for external contributors
- `/pac ok-to-test SHA` - Approve CI for external contributors for a specific SHA

Example:

```text
/pac test
```

You can also configure GitOps command prefix in [Global Repository CR]({{< relref "/docs/operations/global-repository-settings" >}}) so that it will be applied to all Repository CRs those are not defining their own prefix.

## Cancelling a PipelineRun

**What it does:** The `/cancel` command stops running PipelineRuns by commenting on the pull request.

**When to use it:** A pipeline is running against outdated code, or infrastructure issues make the current run pointless. To cancel all PipelineRuns on a PR, add a comment starting with `/cancel`. Pipelines-as-Code cancels every PipelineRun attached to that pull request.

Example:

```text
The infrastructure appears to be experiencing issues. Cancelling the current PipelineRuns.

/cancel
```

To cancel only a specific PipelineRun, append its name to the `/cancel` command.

Example:

```text
The infrastructure appears to be experiencing issues, cancelling this specific pipeline.

/cancel <pipelinerun-name>
```

When you use the GitHub App, Pipelines-as-Code sets the pipeline status to `cancelled`.

![PipelineRun Canceled](/images/pr-cancel.png)

### Cancelling a PipelineRun on a Push Event

You can also cancel a running PipelineRun by commenting directly on the commit rather than on a pull request.

Example:

1. Use `/cancel` to cancel all PipelineRuns.
2. Use `/cancel <pipelinerun-name>` to cancel a specific PipelineRun.

{{< callout type="info" >}}
When you execute GitOps commands on a commit that exists in multiple branches within a push request, Pipelines-as-Code uses the branch with the latest commit.
{{< /callout >}}

This means:

1. If you use `/cancel` without any argument in a comment on a branch, Pipelines-as-Code automatically targets the **main** branch.

   Examples:

   1. `/cancel`
   2. `/cancel <pipelinerun-name>`

2. If you issue `/cancel branch:test`, Pipelines-as-Code targets the commit where you placed the comment but uses the **test** branch.

   Examples:

   1. `/cancel branch:test`
   2. `/cancel <pipelinerun-name> branch:test`

When you use the GitHub App, Pipelines-as-Code sets the pipeline status to `cancelled`.

![GitOps Commits For Comments For PipelineRun Canceled](/images/gitops-comments-on-commit-cancel.png)

This feature is currently supported on GitHub only.

## Passing Parameters to GitOps Commands as Arguments

{{< tech_preview "Passing parameters to GitOps commands as arguments" >}}

**What it does:** You can pass key-value arguments to GitOps commands, overriding [standard dynamic variables]({{< relref "/docs/guides/creating-pipelines#dynamic-variables" >}}) or [custom parameters]({{< relref "/docs/advanced/custom-parameters" >}}) at trigger time.

**When to use it:** You need to change a parameter value for a single run without modifying the PipelineRun definition. For example:

```text
/test pipelinerun1 key=value
```

Pipelines-as-Code sets the custom parameter `key` to `value` (provided `key` is already defined as a custom parameter).

Keep these rules in mind:

- Pipelines-as-Code only parses comments that start with `/`.
- You can only override standard dynamic variables or previously defined custom parameters; you cannot introduce arbitrary new parameters.
- You can place `key=value` pairs anywhere in your comment, and Pipelines-as-Code parses them.

The following formats are accepted, allowing you to pass values with spaces or newlines:

- key=value
- key="a value"
- key="another \"value\" defined"
- key="another
  value with newline"

## Event Type Annotation and Dynamic Variables

**What it does:** Pipelines-as-Code sets the `pipeline.tekton.dev/event-type` annotation on every PipelineRun to indicate which GitOps command triggered it. You can inspect this annotation to understand how a run was initiated.

The possible event types are:

- `test-all-comment`: A `/test` command that tests every matched PipelineRun.
- `test-comment`: A `/test <PipelineRun>` command that tests a specific PipelineRun.
- `retest-all-comment`: A `/retest` command that retests every matched **failed** PipelineRun. If a successful PipelineRun already exists for the same commit, Pipelines-as-Code does not create a new one.
- `retest-comment`: A `/retest <PipelineRun>` command that retests a specific PipelineRun.
- `on-comment`: A custom comment that triggers a PipelineRun.
- `cancel-all-comment`: A `/cancel` command that cancels every matched PipelineRun.
- `cancel-comment`: A `/cancel <PipelineRun>` command that cancels a specific PipelineRun.
- `ok-to-test-comment`: An `/ok-to-test` command that authorizes CI for an external contributor. If a successful PipelineRun already exists for the same commit, Pipelines-as-Code does not create a new one.

If a repository owner comments `/ok-to-test` on a pull request from an external contributor but no PipelineRun **matches** the `pull_request` event (or the repository has no `.tekton/` directory), Pipelines-as-Code sets a **neutral** commit status. This signals that no PipelineRun matched, allowing other workflows -- such as auto-merge -- to proceed without being blocked.

{{< callout type="info" >}}
This neutral check-run status functionality is only supported on GitHub.
{{< /callout >}}

When using the `{{ event_type }}` [dynamic variable]({{< relref "/docs/guides/creating-pipelines#dynamic-variables" >}}) for the following event types:

- `test-all-comment`
- `test-comment`
- `retest-all-comment`
- `retest-comment`
- `cancel-all-comment`
- `ok-to-test-comment`

Pipelines-as-Code returns `pull_request` as the event type instead of the specific categorized GitOps command type. This behavior maintains backward compatibility for users who rely on this dynamic variable.

Pipelines-as-Code currently logs a warning in the repository's matched namespace. A future release will change this behavior to return the specific event type instead.
