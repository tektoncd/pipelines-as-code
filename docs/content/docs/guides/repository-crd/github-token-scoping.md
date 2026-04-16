---
title: GitHub Token Scoping
weight: 3
---

This page explains how to extend the scope of the GitHub token that Pipelines-as-Code generates to cover additional private and public repositories. You need this when your PipelineRun fetches tasks or resources from repositories beyond the one that triggered the event.

By default, the GitHub token is scoped only to the repository where the payload originates.
However, you may need the token to access additional repositories.
For example, you might have a CI repository where the `.tekton/pr.yaml` file and source payload are located, but the build process defined in `pr.yaml` fetches tasks from a separate private CD repository.

You can extend the scope of the GitHub token in two ways:

- _Global configuration_: Extend the GitHub token to a list of repositories in different namespaces. Only administrators can set this configuration.

- _Repository level configuration_: Extend the GitHub token to a list of repositories that exist in the same namespace as the original repository.
Both administrators and non-administrators can set this configuration.

{{< callout type="info" >}}
When using a GitHub webhook, the scoping of the token is what you set when creating your [fine-grained personal access token](https://github.blog/2022-10-18-introducing-fine-grained-personal-access-tokens-for-github/#creating-personal-access-tokens).
{{< /callout >}}

## Prerequisites

- You must have Pipelines-as-Code installed with the **GitHub App** method. Token scoping does not apply to webhook-based installations because the token scope is determined by your personal access token.

| Scoping method | `secret-github-app-token-scoped` setting |
| --- | --- |
| **Global** (`secret-github-app-scope-extra-repos`) | Can stay `true` (default) |
| **Repository-level** (`settings.github_app_token_scope_repos`) | Must be `false` |
| **Both combined** | Can stay `true` |

## Scoping the GitHub token using Global configuration

Use the global configuration to define a list of repositories accessible from any Repository CR in any namespace.
You can specify repositories using exact names or glob patterns (for example, `myorg/*` to match all repositories under an organization where the app is installed).

Unlike repository-level scoping, this global configuration does not require you to disable
`secret-github-app-token-scoped`.

To set the global configuration, add the `secret-github-app-scope-extra-repos` key to the `pipelines-as-code` ConfigMap as shown in the following example:

  ```yaml
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: pipelines-as-code
    namespace: pipelines-as-code
  data:
    secret-github-app-scope-extra-repos: "owner2/project2, owner3/*"
  ```

## Scoping the GitHub token using Repository level configuration

You can also use the Repository CR to scope the generated GitHub token to a list of repositories.
The repositories can be public or private, but must reside in the same namespace as the Repository CR.
You can specify repositories using exact names or glob patterns (for example, `myorg/*` to match all repositories under an organization that has the GitHub App installed).

When you use only `settings.github_app_token_scope_repos`, set
`secret-github-app-token-scoped` to `false`. Otherwise Pipelines-as-Code rejects the
repository-level scope extension request.

Set the `github_app_token_scope_repos` field in the Repository CR spec, as shown in the following example:

  ```yaml
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: test
    namespace: test-repo
  spec:
    url: "https://github.com/linda/project"
    settings:
      github_app_token_scope_repos:
      - "owner/project"
      - "owner1/*"
  ```

In this example, the Repository CR is associated with the `linda/project` repository in the `test-repo` namespace.
Pipelines-as-Code extends the scope of the generated GitHub token to the `owner/project` repository, all repositories matching `owner1/*`, and the `linda/project` repository. All of these repositories must exist in the `test-repo` namespace.

{{< callout type="warning" >}}
If any of the repositories or patterns do not match a repository in the namespace, Pipelines-as-Code fails to scope the GitHub token and returns an error like the following:

```console
failed to scope GitHub token as repo with pattern owner1/project1 does not exist in namespace test-repo
```

{{< /callout >}}

## Combining global and repository level configuration

- When you provide both a `secret-github-app-scope-extra-repos` key in the `pipelines-as-code` ConfigMap and
a `github_app_token_scope_repos` field in the Repository CR, Pipelines-as-Code scopes the token to all the repositories from both configurations, as in the following example:

  - `pipelines-as-code` ConfigMap:

    ```yaml
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: pipelines-as-code
      namespace: pipelines-as-code
    data:
      secret-github-app-scope-extra-repos: "owner2/project2, owner3/project3"
    ```

  - Repository CR:

    ```yaml
     apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
     kind: Repository
     metadata:
       name: test
       namespace: test-repo
     spec:
       url: "https://github.com/linda/project"
       settings:
         github_app_token_scope_repos:
         - "owner/project"
         - "owner1/project1"
    ```

    Pipelines-as-Code scopes the GitHub token to the following repositories: `owner/project`, `owner1/project1`, `owner2/project2`, `owner3/project3`, `linda/project`.

- If you set only the global configuration in the `secret-github-app-scope-extra-repos` key in the `pipelines-as-code` ConfigMap,
Pipelines-as-Code scopes the GitHub token to all the listed repositories, as well as the original repository from which the payload originates.

- If you set only the `github_app_token_scope_repos` field in the Repository CR,
Pipelines-as-Code scopes the GitHub token to all the listed repositories, as well as the original repository from which the payload originates.
All the repositories must exist in the same namespace where the Repository CR is created.

- If the GitHub App is not installed for any repositories that you list in the global or repository level configuration,
token creation fails with an error like the following:

    ```text
    failed to scope token to repositories in namespace test-repo with error : could not refresh installation id 36523992's token: received non 2xx response status \"422 Unprocessable Entity\" when fetching https://api.github.com/app/installations/36523992/access_tokens: Post \"https://api.github.com/repos/savitaashture/article/check-runs\
    ```

- If scoping the GitHub token fails for any reason,
Pipelines-as-Code does not run the CI process. This includes cases where the same repository is listed (or matched) in both the global and repository level configuration,
and the scoping fails at the repository level because the repository is not in the same namespace as the Repository CR.

  In the following example, the `owner5/project5` repository is listed in the global configuration and matches the pattern in the repository level configuration:

  ```yaml
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: pipelines-as-code
    namespace: pipelines-as-code
  data:
    secret-github-app-scope-extra-repos: "owner5/project5"
  ```

  ```yaml
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: test
    namespace: test-repo
  spec:
    url: "https://github.com/linda/project"
    settings:
      github_app_token_scope_repos:
      - "owner5/*"
  ```

  In this example, if the `owner5/project5` repository (or any other repository matching the `owner5/*` pattern) is not in the `test-repo` namespace, Pipelines-as-Code fails to scope the GitHub token with the following error:

  ```yaml
  failed to scope GitHub token as repo with pattern owner5/* does not exist in namespace test-repo
  ```
