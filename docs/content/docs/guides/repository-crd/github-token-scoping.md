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

Here is how Pipelines-as-Code builds the list of repositories for the token:

```mermaid
flowchart LR
%% title: How Pipelines-as-Code builds the list of repositories for a scoped GitHub App token
%% autoplay
  CM@{ img: "/icons/config.svg", label: "Global extra repos", w: 48, h: 48, constraint: "on" }
  Repo@{ img: "/icons/repository.svg", label: "Repository CR scope", w: 48, h: 48, constraint: "on" }
  NS@{ img: "/icons/namespace.svg", label: "Repository CRs in namespace", w: 48, h: 48, constraint: "on" }
  Flag@{ img: "/icons/policy.svg", label: "token-scoped flag", w: 48, h: 48, constraint: "on" }
  List@{ img: "/icons/filter.svg", label: "Repository list", w: 48, h: 48, constraint: "on" }
  GH@{ img: "/icons/api.svg", label: "GitHub App", w: 48, h: 48, constraint: "on" }
  Token@{ img: "/icons/token.svg", label: "Scoped token", w: 48, h: 48, constraint: "on" }
  Fail@{ img: "/icons/refuse.svg", label: "CI not run", w: 48, h: 48, constraint: "on" }
  CM glob@-->|patterns| List
  Repo --> NS
  NS match@-->|matched| Flag
  NS nomatch@-->|no match| Fail
  Flag allow@-->|allowed| List
  Flag deny@-->|true, no global list| Fail
  List -->|plus origin repo| GH --> Token

%% play: Global list
%% skip Repo NS Flag Fail
%% CM -> List @glob : owner3/* | The ConfigMap patterns go into the list as they are
%% List -> GH : origin repo | The repository that sent the event is always added
%% GH -> Token | Globs are expanded against the repositories the App installation can access, then GitHub mints the token
%% play: Repository list
%% skip CM Fail
%% Repo -> NS : owner/project | Each pattern must match a Repository CR in the same namespace
%% NS -> Flag @match
%% Flag -> List @allow | secret-github-app-token-scoped must be false when there is no global list
%% List -> GH : origin repo
%% GH -> Token
%% play: Both
%% skip Fail
%% CM -> List @glob : global | With a global list set, the flag can stay true
%% Repo -> NS : repository
%% NS -> Flag @match
%% Flag -> List @allow | and both lists are merged
%% List -> GH : origin repo
%% GH -> Token
%% play: Pattern not in namespace
%% skip CM Flag List GH Token
%% Repo -> NS : owner5/*
%% NS -> Fail @nomatch : error | No Repository CR in the namespace matches, so token scoping fails and CI does not run
%% play: Flag left on
%% skip CM List GH Token
%% Repo -> NS : owner/project
%% NS -> Flag @match
%% Flag -> Fail @deny : error | With secret-github-app-token-scoped true and no global list, the repository list is rejected
```

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
