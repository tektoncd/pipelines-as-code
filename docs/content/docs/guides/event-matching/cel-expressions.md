---
title: CEL Expressions
weight: 3
---

This page covers CEL (Common Expression Language) expressions, which give you fine-grained control over which Git events trigger your PipelineRuns. Use CEL when simple annotation matching is not flexible enough -- for example, when you want to exclude a specific branch, trigger only when certain file paths change, or filter on payload fields from your Git provider.

{{< callout type="error" >}}
If you use the `on-cel-expression` annotation in the same PipelineRun as `on-event`, `on-target-branch`, `on-label`, `on-path-change`, or `on-path-change-ignore`, the `on-cel-expression` annotation takes priority and Pipelines-as-Code ignores the other annotations.
{{< /callout >}}

## Basic usage

The following example matches a `pull_request` event targeting the `main` branch when the source branch is called `wip`:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && target_branch == "main" && source_branch == "wip"
```

## Available CEL variables

The following fields are available inside CEL expressions:

| **Field** | **Description** |
| --- | --- |
| `event` | `push`, `pull_request`, or `incoming`. |
| `event_type` | The event type from the webhook payload header. This value is provider-specific (for example, GitHub sends `pull_request`, GitLab sends `Merge Request`). |
| `target_branch` | The branch the event targets (for example, `main` in a pull request that merges into `main`). |
| `source_branch` | The branch the pull request originates from. On `push` events, this equals `target_branch`. |
| `target_url` | The URL of the repository the event targets. |
| `source_url` | The URL of the repository the pull request originates from. On `push` events, this equals `target_url`. |
| `event_title` | The title of the event. For `push`, this is the commit title. For pull requests, this is the pull request title. Supported on GitHub, GitLab, and Bitbucket Cloud only. |
| `body` | The full webhook payload body from the Git provider. Example: `body.pull_request.number` retrieves the pull request number on GitHub. |
| `headers` | The full set of webhook headers from the Git provider. Example: `headers['x-github-event']` retrieves the event type on GitHub. |
| `.pathChanged` | A suffix function you append to a glob string to check whether matching paths changed. Not supported on Bitbucket Cloud. |
| `files` | The list of files that changed in the event (`all`, `added`, `deleted`, `modified`, and `renamed`). Example: `files.all` or `files.deleted`. For pull requests, this includes every file in the pull request. |
| Custom params | Any [custom parameters]({{< relref "/docs/advanced/custom-parameters" >}}) you define in the Repository CR `spec.params` are available as CEL variables. Example: `enable_ci == "true"`. See [Custom parameters in CEL expressions limitations](#custom-parameters-in-cel-expressions-limitations) below for important details. |

Because CEL expressions can combine multiple conditions, they enable scenarios that simple annotations cannot handle. For example, to match a `pull_request` event but exclude the `experimental` branch:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && target_branch != "experimental"
```

{{< callout type="info" >}}
You can find more information about the CEL language spec here:

<https://github.com/google/cel-spec/blob/master/doc/langdef.md>
{{< /callout >}}

## Custom parameters in CEL expressions: limitations

### Filtered parameters become undefined

If you use a custom parameter that has a `filter` in a CEL expression, and the filter condition is **not met**, the parameter is **undefined**. This causes a CEL evaluation error rather than evaluating to `false`.

For example, consider this Repository CR:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  params:
    - name: docker_registry
      value: "registry.staging.example.com"
      filter: pac.event_type == "pull_request"
```

And this PipelineRun:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: my-pipeline
  annotations:
    pipelinesascode.tekton.dev/on-cel-expression: |
      docker_registry == "registry.staging.example.com"
spec:
  # ... pipeline spec
```

On a **push event**, the `docker_registry` parameter is not defined (because the filter only matches pull requests), so the CEL expression produces an **error** instead of `false`. Pipelines-as-Code does not evaluate the PipelineRun and reports an error.

To avoid undefined-parameter errors, reference custom parameters only when their filter conditions match, or use parameters without filters for CEL matching. You can test your CEL expressions against different event types with the [tkn pac cel]({{< relref "/docs/cli/" >}}) command to verify they work correctly across all scenarios.

### Custom parameters cannot override built-in variables

Custom parameters you define in the Repository CR cannot override the built-in CEL variables that Pipelines-as-Code provides, such as:

* `event` (or `event_type`)
* `target_branch`
* `source_branch`
* `trigger_target`
* And other default variables documented in the table above

If you define a custom parameter with the same name as a built-in CEL variable, the built-in variable takes precedence. Always choose unique names for your custom parameters to avoid conflicts.

## Matching by regex

You can match any CEL field using a regular expression. For example, to trigger a PipelineRun on `pull_request` events where the `source_branch` contains the substring `feat/`:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && source_branch.matches(".*feat/.*")
```

## Matching by path change

Path-based matching lets you run a PipelineRun only when specific files change -- for example, running documentation tests only when docs change, or skipping CI when only README files are updated.

{{< callout type="info" >}}
Pipelines-as-Code supports two ways to match files changed in a particular event. The `.pathChanged` suffix function supports [glob pattern](https://github.com/gobwas/glob#example) and does not support different types of "changes" (added, modified, deleted, and so on). The other option is the `files.` property (`files.all`, `files.added`, `files.deleted`, `files.modified`, `files.renamed`) which can target specific types of changed files and supports CEL expressions, for example `files.all.exists(x, x.matches('renamed.go'))`.
{{< /callout >}}

{{< callout type="warning" >}}
Both `.pathChanged` and the `files.` properties read the list of changed files from your Git provider. A changed Git submodule appears there as a single path, with none of the files inside it. See [Git submodules]({{< relref "/docs/guides/event-matching/path-matching#git-submodules" >}}). Bitbucket Cloud does not report changed files at all, so `.pathChanged` is always false and `files.` is always empty there.
{{< /callout >}}

To run a PipelineRun only when certain paths change, use the `.pathChanged` suffix function with a [glob pattern](https://github.com/gobwas/glob#example). The following example matches every `.md` file in the `docs` directory:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && "docs/*.md".pathChanged()
```

The following example matches any changed file (added, modified, removed, or renamed) in the `tmp` directory:

```yaml
    pipelinesascode.tekton.dev/on-cel-expression: |
      files.all.exists(x, x.matches('tmp/'))
```

This example matches any newly added file in the `src` or `pkg` directory:

```yaml
    pipelinesascode.tekton.dev/on-cel-expression: |
      files.added.exists(x, x.matches('src/|pkg/'))
```

This example matches modified files named `test.go`:

```yaml
    pipelinesascode.tekton.dev/on-cel-expression: |
      files.modified.exists(x, x.matches('test.go'))
```

## Excluding non-code changes

When you want to run tests only when actual code changes occur -- skipping documentation-only or config-only pull requests -- you can negate a file pattern match:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request"
  && target_branch == "main"
  && !files.all.all(x, x.matches('^docs/') || x.matches('\\.md$') || x.matches('(\\.gitignore|OWNERS|PROJECT|LICENSE)$'))
```

This expression:

* Matches only `pull_request` events targeting the `main` branch.
* **Skips** the PipelineRun when every changed file matches one of these patterns:
  * Files in the `docs/` directory (`^docs/`)
  * Markdown files (`\\.md$`)
  * Common repository metadata files (`\\.gitignore`, `OWNERS`, `PROJECT`, `LICENSE`)

The `!files.all.all(x, x.matches(...))` pattern means "not every file matches these exclusion patterns." In practice, Pipelines-as-Code triggers the PipelineRun only when at least one changed file falls outside the excluded patterns -- that is, when there are meaningful code changes.

{{< callout type="warning" >}}
When you use regex patterns in CEL expressions, escape special characters properly. You must double the backslash (`\\`) within the CEL string context. Using logical OR (`||`) operators within the `matches()` function is more reliable than combining patterns with a pipe (`|`) character in a single regex.
{{< /callout >}}

## Matching by event title

The following example matches all pull requests whose title starts with `[DOWNSTREAM]`:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && event_title.startsWith("[DOWNSTREAM]")
```

For `pull_request` events, `event_title` contains the pull request title. For `push` events, it contains the commit title.

## Matching by body payload

{{< tech_preview "Matching PipelineRun on body payload" >}}

The full webhook payload body from the Git provider is available in the CEL variable `body`. You can use this variable to filter on any field your Git provider sends.

For example, this GitHub-specific expression:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  body.pull_request.base.ref == "main" &&
    body.pull_request.user.login == "superuser" &&
    body.action == "synchronize"
```

matches only when the pull request targets the `main` branch, the pull request author is `superuser`, and the action is `synchronize` (meaning someone pushed new commits to the pull request).

{{< callout type="info" >}}
When you match on the body payload in a pull request, GitOps comments such as `/retest` do not work as expected. The reason is that a comment event delivers the comment payload, not the original pull request payload, so the body fields do not match your CEL expression.

To retest a pull request that uses body-payload matching, push a new SHA to force a fresh pull request event:

```bash
# assuming you are on the branch you want to retest
# and the upstream remote are set
git commit --amend --no-edit && \
  git push --force-with-lease
```

Alternatively, close and reopen the pull request.

{{< /callout >}}

## Matching by request header

You can filter on webhook headers from the Git provider using the CEL variable `headers`. Header names are always lowercase.

For example, to verify the event is a `pull_request` on [GitHub](https://docs.github.com/en/webhooks/webhook-events-and-payloads#delivery-headers):

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  headers['x-github-event'] == "pull_request"
```
