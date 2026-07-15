---
title: Remote Pipelines
weight: 1
---

This page explains how to reference a Pipeline stored outside your repository using the `pipelinesascode.tekton.dev/pipeline` annotation. Use remote pipelines when you want to share a single Pipeline definition across multiple repositories instead of duplicating it in each `.tekton/` directory. For fetching individual **tasks** from Hub or remote URLs, see [Resolver]({{< relref "_index" >}}).

Only one pipeline annotation (`pipelinesascode.tekton.dev/pipeline`) is allowed per PipelineRun. The value of the annotation must contain exactly one pipeline. Numbered annotations like `pipelinesascode.tekton.dev/pipeline-1` are not supported.

An annotation for a remote pipeline looks like this, using a remote URL:

```yaml
pipelinesascode.tekton.dev/pipeline: "https://git.provider/raw/pipeline.yaml
```

## Pipelines inside the repository

You can also reference a Task or Pipeline from a YAML file inside your repository by specifying the path to it. For example:

```yaml
pipelinesascode.tekton.dev/pipeline: "pipelines/my-pipeline.yaml
```

See [Tasks and Pipelines inside the repository](#tasks-or-pipelines-inside-the-repository) for details.

## Hub support for Pipelines

Just as with tasks, you can reference pipelines from [Artifact Hub](https://artifacthub.io) by name. Artifact Hub is a public registry for discovering and sharing Tekton resources.

```yaml
pipelinesascode.tekton.dev/pipeline: "buildpacks"
```

By default, this syntax installs the pipeline from Artifact Hub.

Examples:

```yaml
# Using Artifact Hub (default)
pipelinesascode.tekton.dev/pipeline: "buildpacks"
```

To pin a specific version of the pipeline, append a colon and the version number:

```yaml
# Using specific version from Artifact Hub
pipelinesascode.tekton.dev/pipeline: "buildpacks:0.1"
```

### Custom hub support for Pipelines

If your cluster administrator has [configured]({{< relref "/docs/api/configmap#hub-configuration" >}}) custom Hub catalogs beyond the default Artifact Hub, you can reference them from your template.

```yaml
pipelinesascode.tekton.dev/pipeline: "[customcatalog://buildpacks:0.1]" # this will install buildpacks from the custom catalog configured by the cluster administrator as customcatalog
```

There is no fallback between different hubs. If Pipelines-as-Code does not find a pipeline in the specified hub, the pull request fails.

## Overriding tasks from a remote pipeline on a PipelineRun

{{< tech_preview "Tasks from a remote Pipeline override" >}}

Pipelines-as-Code supports remote task annotations on the remote pipeline. No other annotations such as `on-target-branch`, `on-event`, or `on-cel-expression` are supported on remote pipelines.

To override one of the tasks from the remote pipeline, add a task annotation with the same task name in your PipelineRun annotations. This is useful when you want to use a shared pipeline but substitute one task with a local version.

For example, if your PipelineRun contains these annotations:

```yaml
kind: PipelineRun
metadata:
  annotations:
    pipelinesascode.tekton.dev/pipeline: "https://git.provider/raw/pipeline.yaml"
    pipelinesascode.tekton.dev/task: "./my-git-clone-task.yaml"
```

And the Pipeline that the `pipelinesascode.tekton.dev/pipeline` annotation references at `https://git.provider/raw/pipeline.yaml` contains these annotations:

```yaml
kind: Pipeline
metadata:
  annotations:
    pipelinesascode.tekton.dev/task: "git-clone"
```

In this case, if the `my-git-clone-task.yaml` file in the root directory defines a task named `git-clone`, Pipelines-as-Code uses it instead of the `git-clone` task from the remote pipeline.

{{< callout type="info" >}}
Task overriding only works for tasks referenced by a `taskRef` to a `Name`. Pipelines-as-Code does not override tasks embedded with a `taskSpec`. See the [Tekton documentation](https://tekton.dev/docs/pipelines/pipelines/#adding-tasks-to-the-pipeline) for the differences between `taskRef` and `taskSpec`.
{{< /callout >}}

## Tasks or Pipelines precedence

When tasks or pipelines share the same name, Pipelines-as-Code applies the following precedence rules to determine which definition to use.

For remote tasks, when a `taskRef` references a task name, Pipelines-as-Code tries to find the task in this order:

1. A task matched from the PipelineRun annotations
2. A task matched from the remote Pipeline annotations
3. A task from the `.tekton/` directory and its subdirectories (automatically included)

For a remote Pipeline referenced through a `pipelineRef`, Pipelines-as-Code tries to match a pipeline in this order:

1. The Pipeline from the PipelineRun annotations
2. The Pipeline from the `.tekton/` directory and its subdirectories (automatically included)

## Tasks or Pipelines inside the repository

You can also reference a task or pipeline from a YAML file inside your repository by specifying the path to the file.

To reference a Task YAML file in the repository:

```yaml
pipelinesascode.tekton.dev/task: "[share/tasks/git-clone.yaml]"
```

To reference a Pipeline YAML file in the repository:

```yaml
pipelinesascode.tekton.dev/pipeline: "share/pipelines/build.yaml"
```

Pipelines-as-Code fetches the specified files from the current repository at the SHA where the event originates (the current pull request or branch push).

If there is any error fetching those resources, or if the fetched YAML cannot be parsed as the appropriate Tekton type, Pipelines-as-Code reports an error and does not process the pipeline.

Remote pipelines may also reference tasks in their own repository using a relative path. See [Relative Tasks]({{< relref "/docs/guides/pipeline-resolution#relative-tasks" >}}) for details.
