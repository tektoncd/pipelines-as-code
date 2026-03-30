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

{{< callout type="info" >}}
While each GitHub application requires its own controller, only one
status reconciler ("watcher") component is needed cluster-wide.
{{< /callout >}}

## Automated CI Setup with startpaac

For automated testing environments, [startpaac](https://github.com/pipelines-as-code/startpaac) can install and configure second controllers automatically.

### Prerequisites

- startpaac installed and configured
- Secret files for the second GitHub application

### Automatic Installation in CI Mode

When you run startpaac with the `--ci` flag, it automatically detects and installs the second controller if:

1. The `PAC_SECOND_SECRET_FOLDER` environment variable is set
2. The folder contains the required secret files:
   - `github-application-id`
   - `github-private-key`
   - `webhook.secret`
   - `smee` (optional, for webhook tunneling)

Example:

```bash
# Create secret folder with GitHub App credentials
mkdir -p ~/secrets-second
echo "12345" > ~/secrets-second/github-application-id
echo "...RSA PRIVATE KEY..." > ~/secrets-second/github-private-key
echo "webhook-secret-value" > ~/secrets-second/webhook.secret
echo "https://smee.io/your-channel" > ~/secrets-second/smee

# Export environment variable
export PAC_SECOND_SECRET_FOLDER=~/secrets-second

# Run startpaac in CI mode
cd startpaac
./startpaac --ci -a
```

This will:

- Generate TLS certificates using minica for the second controller domain
- Create Kubernetes secrets from the secret folder
- Deploy the second controller with proper ingress/route configuration
- Configure gosmee for webhook tunneling (if you provided a smee URL)

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
                            [--openshift-route]
                            LABEL
```

#### Key Options

| Option                     | Description                                                                   |
| -------------------------- | ----------------------------------------------------------------------------- |
| `--configmap`              | ConfigMap name (default: `<LABEL>-configmap`)                                 |
| `--secret`                 | Secret name (default: `<LABEL>-secret`)                                       |
| `--ingress-domain`         | Create Ingress with specified domain (Kubernetes)                             |
| `--openshift-route`        | Create OpenShift Route instead of Ingress                                     |
| `--controller-image`       | Custom controller image (use `ko` for local builds)                           |
| `--smee-url`               | Deploy Gosmee sidecar for webhook tunneling                                   |
| `--namespace`              | Target namespace (default: `pipelines-as-code`)                               |

### Example Scenarios

The following examples show common deployment patterns.

- Basic Kubernetes Deployment

```bash
# Generate and apply configuration for GitHub Enterprise
python3 hack/second-controller.py ghe \
  --ingress-domain "ghe.example.com" \
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
PAC_CONTROLLER_IMAGE      Controller image (default: ghcr.io/tektoncd/pipelines-as-code-controller:stable)
```
