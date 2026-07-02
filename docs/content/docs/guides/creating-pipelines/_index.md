---
title: Authoring PipelineRun
weight: 1
---

This page covers how to write PipelineRun definitions that Pipelines-as-Code picks up from your `.tekton/` directory. Use it when you need to define CI/CD pipelines triggered by Git events.

Pipelines-as-Code follows the standard Tekton template format as closely as possible. You write your templates as `.yaml` files in the `.tekton/` directory at the top level of your repository, and Pipelines-as-Code runs them. You can reference YAML files in other repositories using [remote HTTP URLs]({{< relref "/docs/guides/pipeline-resolution#remote-http-url" >}}), but PipelineRuns only trigger from events in the repository that contains the `.tekton/` directory.

Using its [resolver]({{< relref "/docs/guides/pipeline-resolution" >}}), Pipelines-as-Code bundles each PipelineRun with all its referenced Tasks into a single self-contained PipelineRun with no external dependencies.

To check out the commit that triggered the webhook, clone the repository at that ref inside your pipeline. In most cases, you can reuse the [git-clone](https://artifacthub.io/packages/tekton-task/git-clone/git-clone) task from Artifact Hub.

To inject parameters such as the commit SHA and repository URL, Pipelines-as-Code provides [dynamic variables](#dynamic-variables) written as `{{ var }}` that you can use anywhere in your template.

For Pipelines-as-Code to process your PipelineRun, include either an embedded `PipelineSpec` or a separate `Pipeline` object that references a YAML file in the `.tekton/` directory. The Pipeline object can include `TaskSpecs`, which you may define separately as Tasks in another YAML file in the same directory. Give each PipelineRun a unique name to avoid conflicts.

{{< callout type="warning" >}}
PipelineRuns with duplicate names are never matched.
{{< /callout >}}

## Dynamic variables

Dynamic variables are placeholder tokens written as `{{ variable_name }}` that Pipelines-as-Code replaces with real values at runtime. They let you write generic PipelineRun templates that automatically adapt to each event without hardcoding repository URLs, branch names, or commit SHAs.

The following table lists all available dynamic variables. The most commonly used are `revision` and `repo_url`, which provide the commit SHA and repository URL being tested. Use these with the [git-clone](https://artifacthub.io/packages/tekton-task/git-clone/git-clone) task to check out the code under test.

| Variable | Description | Example | Example Output |
| --- | --- | --- | --- |
| body | The full payload body (see [below]({{< relref "/docs/guides/creating-pipelines/cel-expressions#using-the-body-and-headers-in-a-pipelines-as-code-parameter" >}})) | `{{body.pull_request.user.email }}` | <email@domain.com> |
| event | The normalized event type (`push`, `pull_request`, or `incoming`) | `{{event}}` | pull_request |
| event_type | The provider-specific event type from the webhook payload header (eg: GitHub sends `pull_request`, GitLab sends `Merge Request`, etc.) | `{{event_type}}` | pull_request (see the note for GitOps Comments [here]({{< relref "/docs/guides/gitops-commands/advanced#event-type-annotation-and-dynamic-variables" >}}) ) |
| git_auth_secret | The auto-generated secret name containing the provider token for checking out private repositories. | `{{git_auth_secret}}` | pac-gitauth-xkxkx |
| headers | The request headers (see [below]({{< relref "/docs/guides/creating-pipelines/cel-expressions#using-the-body-and-headers-in-a-pipelines-as-code-parameter" >}})) | `{{headers['x-github-event']}}` | push |
| pull_request_number | The pull request or merge request number. Only defined for a `pull_request` event or a push event that occurs when a pull request is merged. | `{{pull_request_number}}` | 1 |
| repo_name | The repository name. | `{{repo_name}}` | pipelines-as-code |
| repo_owner | The repository owner on the Git provider. For providers with owner hierarchies (for example, GitLab orgs, namespaces, groups, and subgroups), this contains the full ownership slug. | `{{repo_owner}}` | openshift-pipelines |
| repo_url | The full repository URL. | `{{repo_url}}` | <https://github.com/openshift-pipelines/pipelines-as-code> |
| revision | The full SHA of the commit. | `{{revision}}` | 1234567890abcdef |
| sender | The username (or account ID on some providers) of the user who triggered the commit. | `{{sender}}` | johndoe |
| source_branch | The branch name where the event originates. | `{{source_branch}}` | main |
| git_tag | The Git tag pushed. Only available for tag push events; otherwise an empty string `""`. | `{{git_tag}}` | v1.0 |
| source_url | The source repository URL where the event originates. For push events, this equals `repo_url`. | `{{source_url}}` | <https://github.com/tektoncd/pipelines-as-code> |
| target_branch | The branch name that the event targets. For push events, this equals `source_branch`. | `{{target_branch}}` | main |
| target_namespace | The target namespace where the Repository CR matched and where Pipelines-as-Code creates the PipelineRun. | `{{target_namespace}}` | my-namespace |
| trigger_comment | The comment that triggered the PipelineRun when you use a [GitOps command]({{< relref "/docs/guides/gitops-commands" >}}) (such as `/test` or `/retest`). | `{{trigger_comment}}` | /merge-pr branch |
| pull_request_labels | The labels on the pull request, separated by a newline character. | `{{pull_request_labels}}` | bugs\nenhancement |

{{< callout type="info" >}}
When you use the `{{ pull_request_number }}` variable in a push-triggered PipelineRun after a pull request merge, the Git provider API may return more than one pull request if the commit is associated with multiple pull requests. In that case, `{{ pull_request_number }}` contains the number of the first pull request the API returns.

The `{{ pull_request_number }}` variable in push events is currently supported only on the GitHub provider.
{{< /callout >}}

### Defining parameters with object values in YAML

When you define parameters in YAML, you may need to pass an object or a dynamic variable (for example, `{{ body }}`) as a parameter value. YAML validation rules prevent such values from appearing inline.

For instance, if you attempt to define a parameter like this:

```yaml
spec:
  params:
    - name: body
      value: {{ body }}  # This will result in a YAML validation error
  pipelineSpec:
    tasks:
```

This produces a YAML validation error because objects or multiline strings cannot appear inline. To fix this, define the value in block format instead:

```yaml
spec:
  params:
    - name: body
      value: |-
        {{ body }}
    # Alternatively, use '>' to specify that the value will be in block format
    - name: pull_request
      value: >
        {{ body.pull_request }}
  pipelineSpec:
    tasks:
```

Using block format avoids validation errors and keeps your YAML properly structured.

## Matching an event to a PipelineRun

Each PipelineRun can match different Git provider events through special annotations on the PipelineRun. This is how you control which pipelines run for which branches and event types.

For example, with the following metadata on your PipelineRun:

```yaml
metadata:
  name: pipeline-pr-main
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[main]"
  pipelinesascode.tekton.dev/on-event: "[pull_request]"
```

Pipelines-as-Code matches the PipelineRun `pipeline-pr-main` if the Git provider event targets the branch `main` and originates from a `pull_request`.

There are many ways to match an event to a PipelineRun. See the [event matching]({{< relref "/docs/guides/event-matching" >}}) page for details on all available matching options.

## Example

Pipelines-as-Code tests itself using this approach. You can see working examples in its [.tekton](https://github.com/tektoncd/pipelines-as-code/tree/main/.tekton) directory.
