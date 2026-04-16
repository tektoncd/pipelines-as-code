---
title: Installation Through Operator
weight: 4
---

This page walks you through installing Pipelines-as-Code on OpenShift using the Red Hat OpenShift Pipelines Operator. Follow these steps if you are running OpenShift rather than standard Kubernetes.

## Prerequisites

Before you begin, ensure that:

- Your cluster runs OpenShift 4.10 or higher.
- You have cluster-admin access to install operators.

## Install

The recommended way to install Pipelines-as-Code on OpenShift is with the [Red Hat OpenShift Pipelines Operator](https://docs.openshift.com/container-platform/latest/cicd/pipelines/installing-pipelines.html). When you install OpenShift Pipelines 1.7.x or later, Pipelines-as-Code is included automatically.

On the OpenShift Pipelines Operator, the default namespace is `openshift-pipelines`.

{{< callout type="warning" >}}
When Pipelines-as-Code is installed through the [Tekton Operator](https://github.com/tektoncd/operator), the configuration is
controlled by the [TektonConfig Custom Resource](https://github.com/tektoncd/operator/blob/main/docs/TektonConfig.md#openshiftpipelinesascode).
The Tekton Operator reverts any configuration changes made directly
to the `pipelines-as-code` ConfigMap or `OpenShiftPipelinesAsCode` custom resource.
{{< /callout >}}

## Configuration

The default configuration for Pipelines-as-Code in `TektonConfig` is shown
below.

{{< callout type="info" >}}
Since version v0.37.0, Pipelines-as-Code defaults to using Artifact
Hub. The public Tekton Hub (hub.tekton.dev) has been deprecated and is no longer
available. You can still use custom self-hosted Tekton Hub instances by
configuring them as custom catalogs (see [Remote Hub Catalogs]({{< relref "/docs/api/configmap#hub-configuration" >}})).
{{< /callout >}}

```yaml
apiVersion: operator.tekton.dev/v1alpha1
kind: TektonConfig
metadata:
  name: config
spec:
  platforms:
    openshift:
      pipelinesAsCode:
        enable: true
        settings:
          bitbucket-cloud-check-source-ip: 'true'
          remote-tasks: 'true'
          application-name: Pipelines-as-Code CI
          auto-configure-new-github-repo: 'false'
          error-log-snippet: 'true'
          error-detection-from-container-logs: 'false'
          enable-cancel-in-progress-on-pull-requests: 'false'
          enable-cancel-in-progress-on-push: 'false'
          skip-push-event-for-pr-commits: 'true'
          hub-url: 'https://artifacthub.io/api/v1'
          hub-catalog-type: 'artifacthub'
          error-detection-max-number-of-lines: '50'
          error-detection-simple-regexp: >-
            ^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+):([
            ]*)?(?P<error>.*)
          secret-auto-create: 'true'
          secret-github-app-token-scoped: 'true'
          remember-ok-to-test: 'true'
```

You can add or update any supported configuration key for Pipelines-as-Code under `settings`. After you change the `TektonConfig` custom resource, the operator updates the `pipelines-as-code` ConfigMap automatically.

{{< callout type="info" >}}
By default, the Tekton Operator installs Pipelines-as-Code. The default value of `enable` is `true` as shown in the following example:
{{< /callout >}}

```yaml
spec:
  platforms:
    openshift:
      pipelinesAsCode:
        enable: true
        settings:

```

## Disabling Pipelines-as-Code

To disable Pipelines-as-Code, set `enable: false` in the `TektonConfig` custom resource:

```yaml
spec:
  platforms:
    openshift:
      pipelinesAsCode:
        enable: false
        settings:

```
