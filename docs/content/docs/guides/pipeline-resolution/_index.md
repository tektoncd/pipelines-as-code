---
title: Resolver
weight: 3
---

This page explains how the Pipelines-as-Code resolver processes your `.tekton/` directory and assembles self-contained PipelineRuns. It covers the overall resolution process and the `pipelinesascode.tekton.dev/task` annotation for fetching tasks from Artifact Hub, HTTP URLs, or your repository. For the `pipelinesascode.tekton.dev/pipeline` annotation that references remote Pipelines, see [Remote Pipelines]({{< relref "remote-pipelines" >}}).

The resolver exists to solve a practical problem: Tekton PipelineRuns can reference external Tasks and Pipelines by name, but those references must be available on the cluster at runtime. Rather than requiring you to pre-install every Task, Pipelines-as-Code resolves all references at submission time and embeds everything into a single PipelineRun. This ensures your pipeline is fully self-contained and portable.

Pipelines-as-Code parses all files ending with `.yaml` or `.yml` in the `.tekton/` directory and its subdirectories at the root of your repository. It automatically detects [Tekton](https://tekton.dev) resources such as `Pipeline`, `PipelineRun`, and `Task`.

When Pipelines-as-Code detects a [PipelineRun](https://tekton.dev/docs/pipelines/pipelineruns/), it *resolves* it into a single PipelineRun with an embedded PipelineSpec containing all referenced [Tasks](https://tekton.dev/docs/pipelines/tasks/) and [Pipelines](https://tekton.dev/docs/pipelines/pipelines/). This embedding ensures that every dependency required for execution is contained within a single PipelineRun at the time it runs on the cluster.

{{< callout type="info" >}}
The Pipelines-as-Code resolver is a different concept from the [Tekton resolver](https://tekton.dev/docs/pipelines/resolution-getting-started/). Both are compatible and you can use the Tekton resolver within a Pipelines-as-Code PipelineRun.
{{< /callout >}}

If any YAML file contains errors, Pipelines-as-Code halts parsing and reports errors on both the Git provider interface and in the event's namespace stream.

The resolver then transforms the Pipeline `Name` field to a `GenerateName` based on the Pipeline name, ensuring each PipelineRun gets a unique name on the cluster.

If you want to split your Pipeline and PipelineRun into separate files, store them in the `.tekton/` directory or its subdirectories. You can also reference `Pipeline` and `Task` resources remotely (see below for how remote tasks work).

The resolver skips the following task types and uses them as-is:

* A reference to a [`ClusterTask`](https://github.com/tektoncd/pipeline/blob/main/docs/tasks.md#task-vs-clustertask)
* A `Task` or `Pipeline` [`Bundle`](https://github.com/tektoncd/pipeline/blob/main/docs/pipelines.md#tekton-bundles)
* A reference to a Tekton [`Resolver`](https://github.com/tektoncd/pipeline/blob/main/docs/pipelines.md#specifying-remote-tasks)
* A [Custom Task](https://github.com/tektoncd/pipeline/blob/main/docs/pipelines.md#using-custom-tasks) with an apiVersion that doesn't have a `tekton.dev/` prefix.

If Pipelines-as-Code cannot resolve the referenced tasks in the `Pipeline` or `PipelineSpec`, the run fails before Pipelines-as-Code applies the PipelineRun to the cluster.

You can see the error on your Git provider interface and in the events of the target namespace where the Repository CR is located.

If you need to test your PipelineRun locally before sending it in a pull request, use the `resolve` command from the `tkn pac` CLI. See the [CLI]({{< relref "/docs/cli/" >}}) documentation for usage details.

## Remote task annotations

Remote task annotations let you pull Task and Pipeline definitions from external sources -- such as Artifact Hub, HTTP URLs, or other repositories -- without committing them to your `.tekton/` directory. Pipelines-as-Code fetches and inlines them during resolution.

If the resolver finds a PipelineRun referencing a remote task or Pipeline through an annotation, it automatically fetches and inlines the resource.

If multiple annotations reference the same task name, the resolver uses the first one it fetches from the annotations.

An annotation for a remote task looks like this:

```yaml
pipelinesascode.tekton.dev/task: "git-clone"
```

Or reference multiple tasks with an array:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone, pylint]"
```

### Hub support for tasks

[Artifact Hub](https://artifacthub.io/packages/search?kind=7&kind=11) is a public registry where the Tekton community publishes reusable Tasks and Pipelines. When you reference a task by name alone, Pipelines-as-Code fetches it from Artifact Hub by default.

```yaml
pipelinesascode.tekton.dev/task: "git-clone"
```

By default, this syntax installs the task from Artifact Hub.

This annotation is Pipelines-as-Code syntax. In your Pipeline or embedded PipelineRun, reference the fetched task by name only:

```yaml
tasks:
  - name: fetch-repository
    taskRef:
      name: git-clone
```

Do not add `resolver: hub` to this `taskRef`. `resolver: hub` is native Tekton resolver syntax; if you combine it with `name: git-clone`, Tekton treats the name as a resolver URI and rejects it because `git-clone` is not a URI.

Examples:

```yaml
# Using Artifact Hub (default)
pipelinesascode.tekton.dev/task: "git-clone"
```

You can specify multiple tasks by separating them with a comma inside bracket array syntax:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone, golang-test, tkn]"
```

You can also use multiple lines by adding a `-NUMBER` suffix to the annotation:

```yaml
  pipelinesascode.tekton.dev/task: "git-clone"
  pipelinesascode.tekton.dev/task-1: "golang-test"
  pipelinesascode.tekton.dev/task-2: "tkn"
```

To pin a specific version of a task, append a colon and the version number:

```yaml
# Using specific version from Artifact Hub
pipelinesascode.tekton.dev/task: "git-clone:0.9.0"
```

#### Native Tekton `hub` resolver syntax for Artifact Hub

If you want Tekton in the cluster to resolve the task from Artifact Hub instead of Pipelines-as-Code fetching and inlining it, use the native `resolver: hub` syntax with resolver parameters and omit `taskRef.name`.

For the current Artifact Hub `git-clone` package, use this form:

```yaml
tasks:
  - name: fetch-repository
    taskRef:
      resolver: hub
      params:
        - name: catalog
          value: git-clone
        - name: type
          value: artifact
        - name: kind
          value: task
        - name: name
          value: git-clone
        - name: version
          value: "1.7.0"
```

Pipelines-as-Code does not inline a task reference that sets `taskRef.resolver`; it leaves that reference for Tekton to resolve in the cluster.

#### Custom hub support for tasks

If your cluster administrator has [configured]({{< relref "/docs/api/configmap#hub-configuration" >}}) custom Hub catalogs beyond the default Artifact Hub, you can reference them from your template.

```yaml
pipelinesascode.tekton.dev/task: "[customcatalog://curl]" # this will install curl from the custom catalog configured by the cluster administrator as customcatalog
```

There is no fallback between different hubs. If Pipelines-as-Code does not find a task in the specified hub, the pull request fails.

There is no support for custom hubs from the CLI when using the `tkn pac resolve` command.

### Remote HTTP URL

If the annotation value starts with `http://` or `https://`, Pipelines-as-Code fetches the task directly from that remote URL:

```yaml
  pipelinesascode.tekton.dev/task: "[https://remote.url/task.yaml]"
```

### Remote HTTP URL from a private repository

If you use the GitHub or GitLab provider and the remote task URL uses the same host as the Repository CR, Pipelines-as-Code uses the provided token to fetch the URL through the GitHub or GitLab API. This lets you reference tasks from private repositories without exposing credentials.

#### GitHub

When you use the GitHub provider and your repository URL looks like this:

<https://github.com/organization/repository>

and the remote HTTP URL is a GitHub "blob" URL:

<https://github.com/organization/repository/blob/mainbranch/path/file>

If the remote HTTP URL has a slash (`/`) in the branch name, you need to URL-encode it with the `%2F` character:

<https://github.com/organization/repository/blob/feature%2Fmainbranch/path/file>

Pipelines-as-Code uses the GitHub API with the generated token to fetch that file, allowing you to reference a task or pipeline from a private repository.

GitHub App tokens are scoped to the owner or organization where the repository is located. If you use the GitHub webhook method instead, you can fetch any private or public repository on any organization where the personal token has access.

You can control this behavior with the `secret-github-app-token-scoped` and `secret-github-app-scope-extra-repos` settings described in the [settings documentation]({{< relref "/docs/api/configmap" >}}).

#### GitLab

The same approach works with GitLab URLs as copied directly from the GitLab UI:

<https://gitlab.com/organization/repository/-/blob/mainbranch/path/file>

or GitLab raw URLs like this:

<https://gitlab.com/organization/repository/-/raw/mainbranch/path/file>

Pipelines-as-Code uses the GitLab token from the Repository CR to fetch the file.

### Tasks inside the repository

You can also reference a task or pipeline from a YAML file inside your repository by specifying the path to it. For example:

```yaml
pipelinesascode.tekton.dev/task: "[share/tasks/git-clone.yaml]"
```

See [Tasks and Pipelines inside the repository]({{< relref "/docs/guides/pipeline-resolution/remote-pipelines#tasks-or-pipelines-inside-the-repository" >}}) for details.

### Relative tasks

Pipelines-as-Code also supports fetching tasks relative to a remote pipeline (see [Remote Pipeline Annotations]({{< relref "/docs/guides/pipeline-resolution/remote-pipelines" >}})). This is useful when a remote pipeline and its tasks live in the same repository and reference each other with relative paths.

Consider the following scenario:

* Repository A (where the event is originating from) contains:

```yaml
# .tekton/pipelinerun.yaml

apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: hello-world
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/on-event: "[push]"
    pipelinesascode.tekton.dev/pipeline: "https://github.com/user/repositoryb/blob/main/pipeline.yaml"
spec:
  pipelineRef:
    name: hello-world
```

* Repository B contains:

```yaml
# pipeline.yaml

apiVersion: tekton.dev/v1beta1
kind: Pipeline
metadata:
  name: hello-world
  annotations:
    pipelinesascode.tekton.dev/task: "./task.yaml"
spec:
  tasks:
    - name: say-hello
      taskRef:
        name: hello
```

```yaml
# task.yaml

apiVersion: tekton.dev/v1beta1
kind: Task
metadata:
  name: hello
spec:
  steps:
    - name: echo
      image: alpine
      script: |
        #!/bin/sh
        echo "Hello, World!"
```

The resolver fetches the remote pipeline and then attempts to retrieve each task. Task paths are relative to the directory where the remote pipeline resides. For example, if the pipeline is at `/foo/bar/pipeline.yaml` and the specified task path is `../task.yaml`, the resolver assembles the target URL as `/foo/task.yaml`.
