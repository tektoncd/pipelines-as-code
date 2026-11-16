---
title: Private Repositories
weight: 6
---
This page explains how Pipelines-as-Code handles authentication for cloning private repositories. Use this information when your PipelineRuns need to access repositories that require credentials.

## Prerequisites

- A working Pipelines-as-Code installation
- A configured Git provider (GitHub App or webhook-based) with appropriate repository access
- The [git-clone](https://artifacthub.io/packages/tekton-task/git-clone/git-clone) task available in your cluster

## How private repository access works

Pipelines-as-Code supports private repositories by automatically creating or
updating a secret in the target namespace. This secret contains the user token
that the [git-clone](https://artifacthub.io/packages/tekton-task/git-clone/git-clone) task
needs to clone private repositories.

When Pipelines-as-Code creates a new PipelineRun in the target namespace,
it also creates a secret with this name format:

`pac-gitauth-REPOSITORY_OWNER-REPOSITORY_NAME-RANDOM_STRING`

This secret contains a [Git Config](https://git-scm.com/docs/git-config) file named
`.gitconfig` and a [Git credentials](https://git-scm.com/docs/gitcredentials)
file named `.git-credentials`. These files configure the base HTTPS URL of the Git provider
(such as <https://github.com>) using the token obtained from the GitHub App
or from a secret attached to the Repository CR when using the webhook method.

The secret also includes the raw token as a key, so you can reuse it in your tasks for
other provider operations.

For a working example, see the [GitHub token usage documentation]({{< relref "/docs/guides/creating-pipelines/github-token" >}}).

The secret has an
[ownerRef](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/)
field pointing to the created PipelineRun. Kubernetes automatically deletes
the secret when you delete the associated PipelineRun.

{{< callout type="warning" >}}
To disable automatic secret creation, set `secret-auto-create` to `false` in
the pipelines-as-code ConfigMap.
{{< /callout >}}

```mermaid
flowchart LR
%% title: Lifecycle of the pac-gitauth secret used to clone a private repository
%% autoplay
  Tok@{ img: "/icons/token.svg", label: "Provider token", w: 48, h: 48, constraint: "on" }
  Ctrl@{ img: "/icons/controller.svg", label: "Controller", w: 48, h: 48, constraint: "on" }
  Sec@{ img: "/icons/secret.svg", label: "pac-gitauth secret", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "PipelineRun", w: 48, h: 48, constraint: "on" }
  Clone@{ img: "/icons/task.svg", label: "git-clone", w: 48, h: 48, constraint: "on" }
  Git@{ img: "/icons/repository.svg", label: "Private repository", w: 48, h: 48, constraint: "on" }
  GC@{ img: "/icons/stop.svg", label: "Secret deleted", w: 48, h: 48, constraint: "on" }
  Tok --> Ctrl
  Ctrl mk@-->|create| Sec
  Ctrl --> Run
  Sec own@-->|ownerRef| Run
  Run -->|basic-auth| Clone -->|HTTPS| Git
  Run gone@-->|deleted| GC
  Ctrl fail@-->|run not created| GC

%% play: Clone a private repository
%% skip GC
%% Tok -> Ctrl : token | The token comes from the GitHub App, or from the secret on the Repository CR for webhooks
%% Ctrl -> Sec @mk : .git-credentials | With secret-auto-create on, a pac-gitauth secret gets .gitconfig, .git-credentials and the raw token
%% Ctrl -> Run | The PipelineRun refers to it through the git_auth_secret variable
%% Sec -> Run @own | Once the run exists, the secret gets an ownerRef pointing to it
%% Run -> Clone : basic-auth | git-clone mounts the secret as the basic-auth workspace
%% Clone -> Git : clone
%% play: PipelineRun deleted
%% skip Tok Clone Git
%% Ctrl -> Sec @mk
%% Ctrl -> Run
%% Sec -> Run @own
%% Run -> GC @gone | Deleting the PipelineRun lets Kubernetes delete the secret it owns
%% play: PipelineRun creation fails
%% skip Run Clone Git
%% Tok -> Ctrl : token
%% Ctrl -> Sec @mk
%% Ctrl -> GC @fail | If the PipelineRun cannot be created, the controller deletes the secret itself
```

## Using the generated token in your PipelineRun

The [git-clone task](https://artifacthub.io/packages/tekton-task/git-clone/git-clone)
expects the secret as a workspace named
`basic-auth` in your PipelineRun.

Add the following workspace reference to your
PipelineRun:

```yaml
spec:
  workspaces:
    - name: basic-auth
      secret:
        secretName: "{{ git_auth_secret }}"
```

Then pass this workspace to the git-clone task inside your
Pipeline or embedded PipelineRun. The following
example shows how to wire the `basic-auth` workspace through to the git-clone task:

```yaml
metadata:
  annotations:
    pipelinesascode.tekton.dev/task: "git-clone"
spec:
  workspaces:
    - name: basic-auth
      secret:
        secretName: "{{ git_auth_secret }}"
    - name: source
      volumeClaimTemplate:
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 1Gi
  pipelineSpec:
    workspaces:
      - name: basic-auth
      - name: source
    tasks:
      - name: git-clone-from-catalog
        taskRef:
          name: git-clone
        params:
          - name: url
            value: "{{ repo_url }}"
          - name: revision
            value: "{{ revision }}"
        workspaces:
          - name: basic-auth
            workspace: basic-auth
          - name: output
            workspace: source
```

- For a complete working example, see the
  [private repository PipelineRun test data](https://github.com/tektoncd/pipelines-as-code/blob/main/test/testdata/pipelinerun_git_clone_private.yaml).

## Fetching remote tasks from private repositories

If your PipelineRun references tasks stored in private repositories, see the [resolver documentation]({{< relref "/docs/guides/pipeline-resolution#remote-http-url-from-a-private-repository" >}}) for configuration details.
