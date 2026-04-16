---
title: "bootstrap"
weight: 2
---

Use `tkn pac bootstrap` to install and configure Pipelines-as-Code with a GitHub App in a single command. This is the fastest way to get a working Pipelines-as-Code setup on a new cluster.

## Usage

```shell
tkn pac bootstrap [flags]
```

## Supported Providers

`tkn pac bootstrap` currently supports the following providers:

* GitHub App on public GitHub
* GitHub App on GitHub Enterprise

## Installation Check

The command first checks whether Pipelines-as-Code is already installed. If not, it prompts you to install the latest stable release using `kubectl`. Add the `--nightly` flag to install the latest CI release instead.

## Route and Endpoint Detection

The bootstrap command automatically detects the OpenShift Route associated with the Pipelines-as-Code controller service and uses it as the endpoint for the GitHub App.

You can use the `--route-url` flag to override the detected OpenShift Route URL or to specify a custom URL on an [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) in a Kubernetes cluster.

Pipelines-as-Code also automatically detects the OpenShift console. On Kubernetes, `tkn pac` attempts to detect the Tekton Dashboard Ingress URL and offers it as the endpoint for the GitHub App.

## Webhook Forwarding with gosmee

If your cluster is not accessible from the internet, you can install a webhook forwarder called [gosmee](https://github.com/chmouel/gosmee). This forwarder enables connectivity between the Pipelines-as-Code controller and GitHub without requiring a public endpoint. It sets up a forwarding URL on <https://hook.pipelinesascode.com> and configures it on GitHub.

On OpenShift, the bootstrap command automatically detects and uses OpenShift Routes, so it does not prompt you to use gosmee. If you need gosmee instead (for example, when running [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview)), specify the `--force-gosmee` flag to bypass Route detection.

{{< callout type="warning" >}}
Do not use gosmee in production environments. It is intended for testing only.
{{< /callout >}}

## bootstrap github-app

Use `tkn pac bootstrap github-app` when you want help creating the GitHub App without running the full interactive bootstrap flow.

This command does not perform the installation flow from `tkn pac bootstrap`. It creates the GitHub App configuration and the corresponding secret for a fresh Pipelines-as-Code setup.

{{< callout type="info" >}}
If Pipelines-as-Code is already installed, the command exits unless you pass `--force-install`.
{{< /callout >}}
