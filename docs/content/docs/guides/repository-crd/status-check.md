---
title: Status Check
weight: 5
---

{{< tech_preview "Status Check settings in Repository CR" >}}

This page explains how to report status checks for PipelineRuns that did not match the incoming event. Use this when you want visibility into which PipelineRuns in your `.tekton/` directory were skipped because their annotations (target branch, event type, CEL expression, or path filter) did not match.

By default, Pipelines-as-Code only reports status for PipelineRuns that matched
and ran. PipelineRuns that did not match are silently ignored. Enabling
`status_check` makes Pipelines-as-Code report a status for each unmatched
PipelineRun so you can see the full picture in your Git provider's UI.

## Configuration

Add the `status_check` block under `spec.settings` in your Repository CR:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  settings:
    status_check:
      enabled: true
      mode: "per_unmatched_pipelinerun"
```

### Fields

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable status check reporting for unmatched PipelineRuns. |
| `mode` | string | | How to report status checks. See [Modes](#modes). |
| `no_match_conclusion` | string | `skipped` | The conclusion to report for unmatched PipelineRuns. Only used when `mode` is `per_unmatched_pipelinerun`. Accepted values: `skipped`, `success`, `neutral`. |
### Modes

#### `per_unmatched_pipelinerun`

Reports a separate status for each PipelineRun that did not match the event.
This is useful when you have multiple PipelineRuns targeting different events
(for example, one for `pull_request` and one for `push`) and you want to see
which ones were skipped on each event.

```yaml
spec:
  settings:
    status_check:
      enabled: true
      mode: "per_unmatched_pipelinerun"
```

### Customizing the conclusion

By default, unmatched PipelineRuns are reported with a `skipped` conclusion.
You can change this to `success` or `neutral` using the `no_match_conclusion`
field:

```yaml
spec:
  settings:
    status_check:
      enabled: true
      mode: "per_unmatched_pipelinerun"
      no_match_conclusion: "success"
```

## Provider behavior

The `skipped` conclusion maps to different states depending on your Git provider:

| Provider | Reported state | Notes |
| --- | --- | --- |
| GitHub App | `skipped` | Shown as a skipped check run. |
| GitHub Webhook | `success` | GitHub commit status API does not support `skipped`. Reported as `success` with a "Skipped" description. |
| GitLab | `skipped` | Shown as a skipped pipeline in the Pipelines tab. |
| Bitbucket Cloud | `STOPPED` | Shown as a stopped build status. |
| Bitbucket Data Center | `UNKNOWN` | Reported with an unknown state. |
| Gitea / Forgejo | `success` | Gitea does not support `skipped`. Reported as `success` with a "Skipped" description. |

## Example

Consider a repository with two PipelineRuns:

- `.tekton/build.yaml` -- targets `pull_request` events on the `main` branch
- `.tekton/deploy.yaml` -- targets `push` events on the `main` branch

When a pull request is opened, `build.yaml` matches and runs. Without
`status_check`, `deploy.yaml` is silently ignored. With it enabled:

```yaml
spec:
  settings:
    status_check:
      enabled: true
      mode: "per_unmatched_pipelinerun"
```

Pipelines-as-Code reports a `skipped` status for `deploy.yaml`, making it
visible in the pull request's status checks that the PipelineRun exists but did
not apply to this event.
