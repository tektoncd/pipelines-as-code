---
title: Multiple GitHub Applications Support
---

{{< tech_preview "Multi-GitHub Apps Support" >}}

This page explains how to run multiple GitHub applications on the same cluster with Pipelines-as-Code. You need this when your organization integrates with more than one GitHub instance -- for example, public GitHub for open-source projects and GitHub Enterprise Server for internal repositories.

## When to Use This

Deploy a second controller when you need to:

- Connect to both public GitHub and a GitHub Enterprise Server instance from the same cluster.
- Isolate credentials and configuration between different GitHub Apps.
- Route webhooks from different GitHub instances to dedicated controllers.

If you only use a single GitHub instance, the default controller is sufficient and you do not need this setup.

## Deployment Architecture

Each GitHub application requires:

1. A dedicated controller deployment
2. An associated Service resource
3. Network exposure via Ingress (Kubernetes) or Route (OpenShift) or [smee.io](https://smee.io) for webhook tunneling
4. Unique configuration through:
   - A Secret containing GitHub App credentials (`private key`, `application_id`, `webhook_secret`)
   - A ConfigMap for application-specific settings

## Controller Configuration Parameters

| Environment Variable         | Description                                        | Example                |
| ---------------------------- | -------------------------------------------------- | ---------------------- |
| `PAC_CONTROLLER_LABEL`       | Unique identifier for the controller instance      | `github-enterprise`    |
| `PAC_CONTROLLER_SECRET`      | Secret containing GitHub App credentials           | `gh-enterprise-secret` |
| `PAC_CONTROLLER_CONFIGMAP`   | ConfigMap with application settings                | `gh-enterprise-config` |

Each controller has its own ConfigMap and therefore its own
`trusted-provider-hostnames` allowlist, which binds a controller and its
credentials to the provider instances it is meant to talk to. Set it in the
ConfigMap named by `PAC_CONTROLLER_CONFIGMAP` when the controller serves a
self-hosted instance:

```bash
kubectl -n pipelines-as-code patch configmap gh-enterprise-config \
  --type merge -p '{"data":{"trusted-provider-hostnames":"ghe.example.com"}}'
```

If the key is left empty, the controller records authenticated provider
hostnames in its ConfigMap's
`pipelinesascode.tekton.dev/auto-trusted-provider-hostnames` annotation. See
[Trusted provider hostnames]({{< relref "settings.md#trusted-provider-hostnames" >}}).

{{< callout type="info" >}}
While each GitHub application requires its own controller, only one
status reconciler ("watcher") component is needed cluster-wide.
{{< /callout >}}

Each controller writes its name, Secret and ConfigMap into the
`pipelinesascode.tekton.dev/controller-info` annotation of the PipelineRuns it
creates. The shared watcher reads that annotation to report back with the right
credentials:

```mermaid
flowchart LR
%% title: Two controllers, one watcher
  GH@{ img: "/icons/globe.svg", label: "github.com", w: 48, h: 48, constraint: "on" }
  GHE@{ img: "/icons/repository.svg", label: "GitHub Enterprise", w: 48, h: 48, constraint: "on" }
  C1@{ img: "/icons/controller.svg", label: "Default controller", w: 48, h: 48, constraint: "on" }
  C2@{ img: "/icons/controller.svg", label: "ghe controller", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "PipelineRun", w: 48, h: 48, constraint: "on" }
  W@{ img: "/icons/monitor.svg", label: "Watcher", w: 48, h: 48, constraint: "on" }
  St@{ img: "/icons/check.svg", label: "Commit status", w: 48, h: 48, constraint: "on" }
  GH -->|webhook| C1 --> Run
  GHE -->|webhook| C2 --> Run
  Run -->|controller-info| W
  W -->|status| St

%% play: GitHub Enterprise
%% skip GH C1
%% GHE -> C2 : event | The GitHub Enterprise App sends its webhooks to the route of the ghe controller
%% C2 -> Run : ghe-secret | It uses its own Secret and ConfigMap and records their names on the PipelineRun
%% Run -> W | The single watcher reads that annotation when the run finishes
%% W -> St : ghe-secret | and reports the result to GitHub Enterprise with the ghe credentials
%% play: Public GitHub
%% skip GHE C2
%% GH -> C1 : event
%% C1 -> Run : default secret
%% Run -> W
%% W -> St : default secret
```

## Automated CI Setup with startpaac

For automated testing environments, [startpaac](https://github.com/pipelines-as-code/startpaac) can install and configure second controllers automatically.

### Prerequisites

- startpaac installed and configured
- A Pipelines-as-Code E2E YAML config file (`PAC_E2E_CONFIG`) with the `github_enterprise` section populated

### Automatic Installation in CI Mode

When you run startpaac with the `--ci` flag, it automatically detects and installs the second controller when `PAC_E2E_CONFIG` (or `--e2e-config FILE`) contains the required `github_enterprise` fields:

- `github_enterprise.application_id`
- `github_enterprise.private_key`
- `github_enterprise.webhook_secret`
- `github_enterprise.smee_url` (optional, for webhook tunneling)
- `github_enterprise.api_url` (optional, normalized to a bare hostname for `trusted-provider-hostnames`)

Example:

```bash
export PAC_E2E_CONFIG=/path/to/e2e-config.yaml

# Run startpaac in CI mode
cd startpaac
./startpaac --ci -a
```

This will:

- Generate TLS certificates using minica for the second controller domain
- Create Kubernetes secrets from `PAC_E2E_CONFIG`
- Deploy the second controller with proper ingress/route configuration
- Configure gosmee for webhook tunneling (if `github_enterprise.smee_url` is set)

### Manual Setup

For manual installation or custom configurations, use the `second-controller.py` script described below.

## Deployment Automation Script

The `second-controller.py` script generates the deployment YAML for a second controller.

**Location:** `./hack/second-controller.py` in the [Pipelines-as-Code repository](https://github.com/tektoncd/pipelines-as-code)

### Basic Usage

```bash
python3 hack/second-controller.py <LABEL> | kubectl apply -f -
```

### Advanced Options

```text
Usage: second-controller.py [-h] [--configmap CONFIGMAP]
                            [--ingress-domain INGRESS_DOMAIN]
                            [--secret SECRET]
                            [--controller-image CONTROLLER_IMAGE]
                            [--gosmee-image GOSMEE_IMAGE]
                            [--smee-url SMEE_URL] [--namespace NAMESPACE]
                            [--trusted-provider-hostnames TRUSTED_PROVIDER_HOSTNAMES]
                            [--openshift-route]
                            LABEL
```

#### Key Options

| Option                         | Description                                                            |
| ------------------------------ | ---------------------------------------------------------------------- |
| `--configmap`                  | ConfigMap name (default: `<LABEL>-configmap`)                          |
| `--secret`                     | Secret name (default: `<LABEL>-secret`)                                |
| `--ingress-domain`             | Create Ingress with specified domain (Kubernetes)                      |
| `--openshift-route`            | Create OpenShift Route instead of Ingress                              |
| `--controller-image`           | Custom controller image (use `ko` for local builds)                    |
| `--smee-url`                   | Deploy Gosmee sidecar for webhook tunneling                            |
| `--namespace`                  | Target namespace (default: `pipelines-as-code`)                        |
| `--trusted-provider-hostnames` | Self-hosted provider hostnames this controller may send credentials to |

### Example Scenarios

The following examples show common deployment patterns.

- Basic Kubernetes Deployment

```bash
# Generate and apply configuration for GitHub Enterprise
python3 hack/second-controller.py ghe \
  --ingress-domain "ghe.example.com" \
  --trusted-provider-hostnames "ghe.example.com" \
  --namespace pipelines-as-code | kubectl apply -f -
```

- OpenShift Deployment with Custom Config

```bash
# Create configuration with custom secret and route
python3 hack/second-controller.py enterprise \
  --openshift-route \
  --secret my-custom-secret \
  --configmap enterprise-config | oc apply -f -
```

- Local Development with Ko

```bash
# Build and deploy controller image using ko
export KO_DOCKER_REPO=quay.io/your-username
ko apply -f <(
  python3 hack/second-controller.py dev \
  --controller-image=ko \
  --namespace pipelines-as-code
)
```

**4. Webhook Tunneling with [Smee.io](https://smee.io)**

Use tunneling when your cluster is not directly reachable from the internet.

```bash
# Deploy with webhook tunneling for local testing
python3 hack/second-controller.py test \
  --smee-url https://smee.io/your-channel | kubectl apply -f -
```

### Environment Variables

The script respects these environment variables for customization:

```text
PAC_CONTROLLER_LABEL      Controller identifier
PAC_CONTROLLER_TARGET_NS  Target namespace (default: pipelines-as-code)
PAC_CONTROLLER_SECRET     Secret name (default: <LABEL>-secret)
PAC_CONTROLLER_CONFIGMAP  ConfigMap name (default: <LABEL>-configmap)
PAC_CONTROLLER_SMEE_URL   Smee.io URL for webhook tunneling
PAC_CONTROLLER_IMAGE      Controller image (default: ghcr.io/tektoncd/pipelines-as-code/pipelines-as-code-controller:stable)
```
