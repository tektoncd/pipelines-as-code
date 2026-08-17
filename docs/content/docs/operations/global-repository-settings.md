---
title: Global Repository Settings
weight: 2
---
{{< tech_preview "Global repository settings" >}}

This page explains how to configure a global Repository CR whose settings apply as defaults across all repositories on your cluster. Use this when you want to define shared configuration -- such as concurrency limits, Git provider credentials, or custom parameters -- in one place instead of repeating it in every namespace.

## How Inheritance Works

Pipelines-as-Code follows a two-tier inheritance model for Repository CR settings. A global Repository CR acts as a fallback: any setting you define there applies to every repository on the cluster unless a namespace-level Repository CR explicitly overrides it. When a namespace-level Repository CR does define a setting, that local value takes precedence over the global one.

This means you can set organization-wide defaults in the global Repository CR and let individual teams override only the settings they need.

## Creating the Global Repository CR

Create the global Repository CR in the namespace where the `pipelines-as-code` controller runs (typically `pipelines-as-code` or `openshift-pipelines`).

The global Repository CR does not need a real `spec.url`. You can leave the field blank or point it to a placeholder URL such as `https://pac.global.repo`.

By default, name the global Repository CR `pipelines-as-code`. If you need a different name, set the `PAC_CONTROLLER_GLOBAL_REPOSITORY` environment variable on both the controller and watcher Deployments.

## Available Global Settings

You can define the following settings in the global Repository CR:

- [Concurrency Limit]({{< relref "/docs/guides/repository-crd/concurrency" >}}).
- [PipelineRun Provenance]({{< relref "/docs/guides/repository-crd#pipelinerun-definition-provenance" >}}).
- [Repository Policy]({{< relref "/docs/advanced/policy-authorization" >}}).
- [Repository GitHub App Token Scope]({{< relref "/docs/guides/repository-crd/github-token-scoping#scoping-the-github-token-using-global-configuration" >}}).
- Git provider auth settings such as user, token, URL, etc.
  - The `type` must be defined in the namespace repository settings and must match the `type` of the global repository (see below for an example).
- [Custom Parameters]({{< relref "/docs/advanced/custom-parameters" >}}).
- [Incoming Webhooks Rules]({{< relref "/docs/advanced/incoming-webhooks" >}}).

{{< callout type="info" >}}
Global settings are only applied when running via a Git provider event; they are not applied when for example using the `tkn pac` cli.
{{< /callout >}}

## Example: How Inheritance Applies

Consider a namespace-level Repository CR in the `user-namespace` namespace:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: repo
  namespace: user-namespace
spec:
  url: "https://my.git.com"
  concurrency_limit: 2
  git_provider:
    type: gitlab
```

And a global Repository CR in the controller namespace:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
spec:
  url: "https://paac.repo"
  concurrency_limit: 1
  params:
    - name: custom
      value: "value"
  git_provider:
    type: gitlab
    secret:
      name: "gitlab-token"
    webhook_secret:
      name: gitlab-webhook-secret
```

In this example, the `repo` Repository CR keeps its concurrency limit of 2 because the local setting overrides the global value of 1. The `custom` parameter with value `value` applies to every repository that does not define its own custom parameters.

Because the local Repository CR sets `git_provider.type` to `gitlab`, matching the global Repository CR, Pipelines-as-Code uses the Git provider settings (secret, webhook secret) from the global repository. It fetches the referenced secrets from the namespace where the global repository lives.

A namespace-level Repository that inherits `git_provider.secret` cannot set a different `git_provider.url`. Otherwise, someone who controls that Repository could redirect a credential owned by the controller namespace. To use another provider endpoint, configure `git_provider.secret` on the namespace-level Repository as well. This restriction applies to webhook-based providers; GitHub Apps use the controller's GitHub App credentials instead of inheriting this secret.

## Webhook-Based Provider Global Settings

The `spec.git_provider.type` field identifies which Git provider handles incoming webhooks. You can set it to any of the following values for webhook-based providers (everything except GitHub Apps):

- github (means a repository configured using [GitHub webhooks]({{< relref "/docs/providers/github-webhook" >}}))
- gitlab
- forgejo
- gitea (alias for forgejo, kept for backwards compatibility)
- bitbucket-cloud
- bitbucket-datacenter

The global Repository CR currently supports only one provider type per cluster. If you need to use a different provider for a specific repository, specify the full provider configuration in that repository's namespace-level Repository CR.
