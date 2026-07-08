---
title: "Repository CR"
weight: 1
---

This page describes the Repository custom resource (CR), which is the central configuration object for Pipelines-as-Code. Every Git repository you onboard to Pipelines-as-Code requires a corresponding Repository CR that defines the connection, authentication, and behavioral settings.

## Overview

The Repository CR is a namespaced Kubernetes resource. You can reference it using the short name `repo`.

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repository
  namespace: my-namespace
spec:
  url: "https://github.com/owner/repo"
  git_provider:
    type: github
    secret:
      name: github-token
      key: token
```

## Resource structure

{{< param name="apiVersion" type="string" required="true" id="param-api-version" >}}
Identifies the API version for the Repository resource. Must be `pipelinesascode.tekton.dev/v1alpha1`.
{{< /param >}}

{{< param name="kind" type="string" required="true" >}}
Identifies the resource kind. Must be `Repository`.
{{< /param >}}

{{< param name="metadata" type="ObjectMeta" required="true" >}}
Standard Kubernetes metadata, including name, namespace, labels, and annotations.
{{< /param >}}

{{< param name="spec" type="RepositorySpec" required="true" >}}
Defines the desired behavior of the repository. See [Repository Spec]({{< relref "repository-spec" >}}) for detailed field documentation.
{{< /param >}}

## Resource shortcuts

The Repository CR supports the following short name:

- `repo` - Short form for `repository`

```bash
# List all repositories
kubectl get repo

# Describe a specific repository
kubectl describe repo my-repository
```

## Custom columns

When you list repositories with `kubectl get repo`, the following columns appear:

- **URL** -- The Git repository URL
- **Succeeded** -- Success status of the last PipelineRun
- **Reason** -- Reason for the last PipelineRun status
- **StartTime** -- When the last PipelineRun started
- **CompletionTime** -- When the last PipelineRun completed

## Complete example

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: example-repo
  namespace: pipelines-as-code
  labels:
    app: my-app
spec:
  url: "https://github.com/organization/repository"
  concurrency_limit: 5
  git_provider:
    type: github
    url: "https://github.com"
    user: "pac-bot"
    secret:
      name: github-app-secret
      key: github-app-token
    webhook_secret:
      name: webhook-secret
      key: webhook-token
  settings:
    pipelinerun_provenance: "source"
    policy:
      ok_to_test:
        - "trusted-user"
        - "maintainer"
      pull_request:
        - "contributor"
    github:
      comment_strategy: "update"
```

## Related resources

- [Repository Spec]({{< relref "repository-spec" >}}) -- Detailed specification fields
- [Settings Reference]({{< relref "settings" >}}) -- Settings configuration options
