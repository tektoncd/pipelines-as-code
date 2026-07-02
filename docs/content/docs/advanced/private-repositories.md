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
