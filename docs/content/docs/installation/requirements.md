---
title: "Requirements"
weight: 2
---

This page lists the prerequisites you must meet before installing Pipelines-as-Code on Kubernetes or OpenShift. Review these requirements to ensure a smooth installation.

## Cluster Requirements

### Kubernetes

For standard Kubernetes clusters:

- **Kubernetes version**: 1.27 or higher (recommended: latest stable)
- **Cluster access**: Admin permissions to install CRDs and cluster roles
- **Resources**: Minimum cluster resources:
  - 2 CPU cores available
  - 4 GB RAM available
  - Persistent storage (optional, for workspace caching)

Pipelines-as-Code has been tested on the following Kubernetes distributions:

- Kind (Kubernetes in Docker)

### OpenShift Clusters

For OpenShift clusters:

- **OpenShift version**: 4.10 or higher
- **OpenShift Pipelines**: Version 1.7.x or higher (includes Tekton Pipelines)
- **Cluster access**: Admin permissions for operator installation or manual installation

## Tekton Pipelines

Pipelines-as-Code depends on Tekton Pipelines for running CI/CD workloads. You must install Tekton Pipelines on your cluster before installing Pipelines-as-Code.

### Version Requirements

- **Minimum version**: Tekton Pipelines v0.50.0
- **Recommended**: Latest stable release

Check Version

Install Latest

```bash
kubectl get deployment tekton-pipelines-controller -n tekton-pipelines -o jsonpath='{.metadata.labels.app\.kubernetes\.io/version}'
```

On OpenShift with the OpenShift Pipelines Operator installed, Tekton Pipelines is included automatically.

## Command-Line Tools

You need the following tools to interact with your cluster and manage Pipelines-as-Code resources.

### Required Tools

1. `kubectl`

The Kubernetes command-line tool for cluster access.

**Installation**: [kubectl installation guide](https://kubernetes.io/docs/tasks/tools/)

**Verify**:

```bash
kubectl version --client
```

1. `oc` (OpenShift only)

The OpenShift command-line tool.

**Installation**: [OpenShift CLI tools](https://docs.openshift.com/container-platform/latest/cli_reference/openshift_cli/getting-started-cli.html)

**Verify**:

```bash
oc version
```

### Optional Tools

These tools enhance the Pipelines-as-Code experience:

1. `tkn`

The Tekton CLI for managing Tekton resources.

**Installation**: [Tekton CLI installation](https://tekton.dev/docs/cli/)

**Install via Homebrew**:

```bash
brew install tektoncd-cli
```

**Verify**:

```bash
tkn version
```

1. `tkn-pac`

The Pipelines-as-Code CLI plugin for `tkn`.

**Install via Homebrew**:

```bash
brew install --cask openshift-pipelines/pipelines-as-code/tektoncd-pac
```

**Install via Direct Download** (Linux):

```bash
curl -L https://github.com/tektoncd/pipelines-as-code/releases/latest/download/tkn-pac-linux-amd64 -o tkn-pac
chmod +x tkn-pac
sudo mv tkn-pac /usr/local/bin/
```

**Install via Direct Download** (macOS):

```bash
curl -L https://github.com/tektoncd/pipelines-as-code/releases/latest/download/tkn-pac-darwin-amd64 -o tkn-pac
chmod +x tkn-pac
sudo mv tkn-pac /usr/local/bin/
```

**Verify**:

```bash
tkn pac version
```

## Git Provider

A Git provider is a hosting service for Git repositories (such as GitHub, GitLab, or Bitbucket). Pipelines-as-Code integrates with Git providers to trigger pipelines based on repository events.

### Supported Git Providers

#### GitHub

- **GitHub.com**: Public GitHub (fully supported)
- **GitHub Enterprise**: Self-hosted GitHub (fully supported)
- **Integration methods**:
  - GitHub App (recommended)
  - Webhook

**Requirements**:

- Repository admin access for webhook configuration
- GitHub App creation permissions (for GitHub App method)
- Ability to create Personal Access Tokens or GitHub App credentials

#### GitLab

- **GitLab.com**: Public GitLab (fully supported)
- **GitLab Self-Managed**: Self-hosted GitLab (fully supported)
- **Integration method**: Webhook

**Requirements**:

- Repository maintainer access
- Ability to create Project Access Tokens or Personal Access Tokens

#### Bitbucket

- **Bitbucket Cloud**: (fully supported)
- **Bitbucket Data Center**: Self-hosted Bitbucket (fully supported)
- **Integration method**: Webhook

**Requirements**:

- Repository admin access
- Ability to create App Passwords (Bitbucket Cloud) or Personal Access Tokens (Bitbucket Data Center)

#### Forgejo

- **Status**: Tech Preview
- **Integration method**: Webhook

**Requirements**:

- Repository admin access
- Ability to create Access Tokens

{{< callout type="warning" >}}
Forgejo support is in Tech Preview and may have limitations or bugs. It is not recommended for production use.
{{< /callout >}}

## Network Requirements

### Ingress/Route

Pipelines-as-Code requires an externally accessible URL to receive webhooks from Git providers. An Ingress is a Kubernetes resource that exposes HTTP/HTTPS routes from outside the cluster to services within it.

#### Kubernetes Ingress

You need one of the following to expose the controller:

- **Ingress Controller**: Nginx, Traefik, GKE Ingress, etc.
- **Load Balancer**: Cloud provider load balancer service
- **Webhook Forwarder**: For development/testing (for example, gosmee, ngrok, or smee.io)

#### OpenShift Route

- **Route**: Automatically created during installation
- No additional configuration required

### Firewall Rules

Ensure the following network connectivity:

- **Inbound**: Git provider webhooks must reach the Pipelines-as-Code controller
  - Port: 443 (HTTPS) or 80 (HTTP)
  - Source: Git provider IP ranges
- **Outbound**: Controller must reach:
  - Git provider API endpoints (for status updates)
  - Artifact Hub or Tekton Hub (for remote task resolution)
  - Container registries (if using remote tasks)

### Git Provider IP Ranges

For firewall configuration, refer to your Git provider’s documentation:

- **GitHub**: [GitHub IP addresses](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/about-githubs-ip-addresses)
- **GitLab.com**: [GitLab IP ranges](https://docs.gitlab.com/ee/user/gitlab_com/)
- **Bitbucket Cloud**: [Bitbucket IP ranges](https://support.atlassian.com/bitbucket-cloud/docs/what-are-the-bitbucket-cloud-ip-addresses-i-should-use-to-configure-my-corporate-firewall/)

For Bitbucket Cloud, you can enable IP source verification in the Pipelines-as-Code configuration to validate that webhooks originate from Bitbucket’s IP ranges.

## Storage Requirements

### For Pipelines-as-Code Components

The Pipelines-as-Code components have minimal storage requirements:

- **ConfigMaps**: < 1 MB
- **Secrets**: < 1 MB
- **Container images**: ~500 MB total for all three components

### For Pipeline Workspaces

Pipeline workspaces may require persistent storage:

- **EmptyDir**: No persistent storage required (ephemeral)
- **PersistentVolumeClaim**: Requires a StorageClass (a Kubernetes resource that defines how persistent volumes are dynamically provisioned) with dynamic provisioning enabled
- **Recommended**: 5-10 GB per concurrent pipeline for caching dependencies

## Permissions

### Cluster Permissions

Installing Pipelines-as-Code requires:

- **Kubernetes**: `cluster-admin` role or equivalent
- **OpenShift**: `cluster-admin` role or equivalent

Permissions needed:

- Create and manage CRDs
- Create ClusterRoles and ClusterRoleBindings
- Create namespaces
- Deploy controllers with elevated permissions

### User Permissions

For users creating Repository CRs in their namespace:

- **Minimum**: A RoleBinding to `openshift-pipeline-as-code-clusterrole` in the target namespace
- Ability to create Secrets in the namespace
- Ability to read and list PipelineRuns

## Optional Dependencies

### Tekton Dashboard

For enhanced log viewing and PipelineRun visualization:

- **Tekton Dashboard**: Latest stable version
- **Installation**: [Tekton Dashboard docs](https://github.com/tektoncd/dashboard)

Install Tekton Dashboard

Configure Integration

```console
kubectl apply -f https://storage.googleapis.com/tekton-releases/dashboard/latest/release.yaml
```

### Container Registry

For building and storing container images in pipelines:

- **Docker Hub**: Public or private repositories
- **Quay.io**: Red Hat’s container registry
- **GCR, ECR, ACR**: Cloud provider registries
- **Harbor**: Self-hosted registry

Store authentication credentials as Kubernetes Secrets.

## Version Compatibility Matrix

| Pipelines-as-Code | Tekton Pipelines | Kubernetes | OpenShift | Go Version |
| --- | --- | --- | --- | --- |
| v0.30.x+ | v0.50.0+ | 1.27+ | 4.10+ | 1.25+ |
| v0.25.x - v0.29.x | v0.44.0+ | 1.23+ | 4.10+ | 1.22+ |

Based on go.mod, Pipelines-as-Code is currently built with Go 1.25.0 and requires Tekton Pipelines v1.7.0.

## Development Requirements

For development or contributing to Pipelines-as-Code:

- **Go**: Version 1.25.0 or higher
- **ko**: For building container images
- **make**: For running build tasks
- **git**: Version control

## Browser Compatibility

For using the Pipelines-as-Code GitHub App creation flow:

- Modern browsers with JavaScript enabled
- Support for OAuth redirect flows

## Summary Checklist

Before proceeding with installation:

- Kubernetes 1.27+ or OpenShift 4.10+ cluster
- Tekton Pipelines v0.50.0+ installed
- kubectl (and oc for OpenShift) CLI installed
- Cluster admin access
- Git provider account with repository access
- Ingress controller or Route capability (or webhook forwarder for testing)
- Network connectivity for webhooks
- Optional: tkn and tkn-pac CLI tools

## Next Steps

After you verify all prerequisites:

1. Choose your installation method:
   - [Kubernetes Installation]({{< relref "kubernetes" >}})
   - [OpenShift Installation]({{< relref "openshift" >}})
2. Review the [Installation Overview]({{< relref "overview" >}})
3. Prepare your Git provider credentials
