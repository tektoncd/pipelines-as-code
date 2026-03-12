# Tekton Pipelines-as-Code

[![Latest Release](https://img.shields.io/github/v/release/tektoncd/pipelines-as-code)](https://github.com/tektoncd/pipelines-as-code/releases/latest)
[![Container Repository on GHCR](https://img.shields.io/badge/GHCR-image-87DCC0.svg?logo=GitHub)](https://github.com/tektoncd/pipelines-as-code/pkgs/container/pipelines-as-code)
[![Go Report Card](https://goreportcard.com/badge/tektoncd/pipelines-as-code)](https://goreportcard.com/report/tektoncd/pipelines-as-code)
[![E2E Tests](https://github.com/tektoncd/pipelines-as-code/actions/workflows/e2e.yaml/badge.svg)](https://github.com/tektoncd/pipelines-as-code/actions/workflows/e2e.yaml)
[![License](https://img.shields.io/github/license/tektoncd/pipelines-as-code)](LICENSE)

<img src="docs/static/images/pac-logo-with-tagline-small.png" alt="PAC LOGO" width="300" align="right"/>

Pipelines-as-Code is an opinionated CI/CD solution for Tekton that allows you to define and manage your pipelines directly from your source code repository.

## Overview

Pipelines-as-Code brings the [Pipelines-as-Code methodology](https://teamhub.com/blog/understanding-pipeline-as-code-in-software-development/) to Tekton. It provides a simple and declarative way to define your pipelines in your Git repository and have them automatically executed on your Kubernetes cluster. It integrates seamlessly with Git providers like GitHub, GitLab, Bitbucket, and Forgejo, and provides feedback directly on your pull requests and commits.

## Why Pipelines-as-Code?

Traditional CI/CD systems often require you to configure pipelines through web interfaces or separate configuration repositories. Pipelines-as-Code changes this by:

- **Version Control**: Your pipeline definitions live alongside your code, so they're versioned, reviewed, and evolved together
- **GitOps Native**: Perfect fit for GitOps workflows where everything is defined as code and managed through Git
- **Developer Experience**: Developers can modify pipelines using familiar Git workflows instead of learning separate CI/CD interfaces
- **Review Process**: Pipeline changes go through the same pull request review process as your application code
- **Branch-specific Pipelines**: Different branches can have different pipeline configurations for feature development
- **No Vendor Lock-in**: Portable Tekton-based pipelines that work across any Kubernetes cluster

## How it Works

Pipelines-as-Code follows a simple event-driven workflow:

1. **Git Event**: A developer pushes code, opens a pull request, or creates a tag
2. **Event Detection**: Pipelines-as-Code receives the webhook from your Git provider (GitHub, GitLab, etc.)
3. **Repository Scan**: PAC looks for a `.tekton/` directory in your repository
4. **Pipeline Resolution**: Found pipeline definitions are processed and resolved (including remote tasks from Tekton Hub)
5. **Execution**: PipelineRuns are created and executed on your Kubernetes cluster
6. **Feedback**: Results are reported back to your Git provider as status checks, PR comments, or commit statuses

The system supports advanced features like:

- Conditional execution based on file changes
- CEL expression language support for event matching
- Template variable substitution (e.g. repo URL, commit SHA, branch name)
- Secret management for secure operations
- Authorization controls to restrict pipeline execution to authorized users (repo admins, members, etc.)
- Automatic cancellation of running PipelineRuns when new events occur
- Incoming webhooks for manual pipeline triggering
- Automatic cleanup of completed PipelineRuns

## Prerequisites

Before getting started with Pipelines-as-Code, ensure you have:

- **Kubernetes cluster**: Version 1.27+ recommended
- **Tekton Pipelines**: Version 0.50.0+ (latest stable recommended)
- **Git Provider**: One of:
  - GitHub (GitHub App or Webhook)
  - GitLab (Webhook)
  - Forgejo (Webhook) - Tech Preview
  - Bitbucket Cloud/Data Center (Webhook)
- **CLI Tool**: `kubectl` for cluster access
- **Optional**: `tkn` CLI for Tekton operations

## Key Features

- **Git-based workflow**: Define your Tekton pipelines in your Git repository and have them automatically triggered on Git events like push, pull request, and comments.
- **Multi-provider support**: Works with GitHub (via GitHub App & Webhook), GitLab, Forgejo (Tech Preview), Bitbucket Data Center & Cloud via webhooks.
- **Annotation-driven workflows**: Target specific events, branches, or CEL expressions and gate untrusted PRs with `/ok-to-test` and `OWNERS`; see [Running the PipelineRun](https://pipelinesascode.com/docs/guide/running/).
- **ChatOps style control**: `/test`, `/retest`, `/cancel`, and branch or tag selectors let you rerun or stop PipelineRuns from PR comments or commit messages; see [GitOps Commands](https://pipelinesascode.com/docs/guide/gitops_commands/).
- **Skip CI support**: Use `[skip ci]`, `[ci skip]`, `[skip tkn]`, or `[tkn skip]` in commit messages to skip automatic PipelineRun execution for documentation updates or minor changes; GitOps commands can still override and trigger runs manually; see [Skip CI Commands](https://pipelinesascode.com/docs/guide/gitops_commands/#skip-ci-commands).
- **Feedback**: GitHub Checks capture per-task timing, log snippets, and optional error annotations while redacting secrets; see [PipelineRun status](https://pipelinesascode.com/docs/guide/statuses/).
- **Inline resolution**: The resolver bundles `.tekton/` resources, inlines remote tasks from Artifact Hub or Tekton Hub, and validates YAML before cluster submission; see [Resolver](https://pipelinesascode.com/docs/guide/resolver/).
- **CLI**: `tkn pac` bootstraps installs, manages Repository CRDs, inspects logs, and resolves runs locally; see the [CLI guide](https://pipelinesascode.com/docs/guide/cli/).
- **Automated housekeeping**: Keep namespaces tidy with the `pipelinesascode.tekton.dev/max-keep-runs` annotation or global settings, and automatically cancel running PipelineRuns when new commits are pushed to the same branch; see [PipelineRuns Cleanup](https://pipelinesascode.com/docs/guide/cleanups/) and [Cancel in progress](https://pipelinesascode.com/docs/guide/running/#cancelling-in-progress-pipelineruns).

## Use Cases

Pipelines-as-Code is perfect for various CI/CD scenarios:

### **Application CI/CD**

- **Multi-language support**: Build and test Go, Python, Node.js, Java applications
- **Container workflows**: Build, scan, and push container images
- **Multi-environment deployments**: Deploy to dev, staging, and production environments

### **GitOps Workflows**

- **Infrastructure as Code**: Validate and apply Terraform, Helm charts, or Kubernetes manifests
- **Configuration management**: Sync application configs across environments
- **Compliance checking**: Run security scans and policy validation

### **Developer Experience**

- **Pull Request validation**: Run comprehensive test suites on every PR
- **Branch-specific builds**: Different pipeline configurations for feature branches
- **Dependency management**: Automated security scanning and dependency updates

### **Advanced Scenarios**

- **Monorepo support**: Trigger specific pipelines based on changed paths
- **Integration testing**: Multi-service testing with databases and external services
- **Release automation**: Automated tagging, changelog generation, and artifact publishing

## Quick Examples

Here's a simple example of a Tekton pipeline triggered by pull requests using Pipelines as Code:

```yaml
# .tekton/pull-request.yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: pr-build
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
spec:
  pipelineSpec:
    tasks:
    - name: fetch-repository
      taskRef:
        name: git-clone
        resolver: hub
      workspaces:
      - name: output
        workspace: source
      params:
      - name: url
        value: "{{ repo_url }}"
      - name: revision
        value: "{{ revision }}"
    - name: run-tests
      runAfter: [fetch-repository]
      taskRef:
        name: golang-test
        resolver: hub
      workspaces:
      - name: source
        workspace: source
  workspaces:
  - name: source
    emptyDir: {}
```

Note: you can generate complete PipelineRun YAML using `tkn-pac` cli like below:

```console
$ tkn pac generate
? Enter the Git event type for triggering the pipeline:  Pull Request
? Enter the target GIT branch for the Pull Request (default: main):  main
ℹ Directory .tekton has been created.
✓ A basic template has been created in .tekton/pull-request.yaml, feel free to customize it.
ℹ You can test your pipeline by pushing the generated template to your git repository
```

This pipeline will automatically run on every pull request to the `main` branch, fetch the code, and run tests.

### More Examples

**Python Application with Testing:**

```yaml
# .tekton/python-ci.yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: python-ci
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request, push]"
    pipelinesascode.tekton.dev/on-target-branch: "[main, develop]"
spec:
  pipelineSpec:
    tasks:
    - name: fetch-source
      taskRef:
        name: git-clone
        resolver: hub
      workspaces:
      - name: output
        workspace: source
      params:
      - name: url
        value: "{{ repo_url }}"
      - name: revision
        value: "{{ revision }}"
    - name: python-test
      runAfter: [fetch-source]
      taskRef:
        name: python-test
        resolver: hub
      workspaces:
      - name: source
        workspace: source
      params:
      - name: requirements_file
        value: "requirements.txt"
      - name: python_version
        value: "3.11"
  workspaces:
  - name: source
    emptyDir: {}
```

**Container Build and Push:**

```yaml
# .tekton/build-push.yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: build-push
  annotations:
    pipelinesascode.tekton.dev/on-event: "[push]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
spec:
  pipelineSpec:
    tasks:
    - name: fetch-source
      taskRef:
        name: git-clone
        resolver: hub
      workspaces:
      - name: output
        workspace: source
      params:
      - name: url
        value: "{{ repo_url }}"
      - name: revision
        value: "{{ revision }}"
    - name: build-push
      runAfter: [fetch-source]
      taskRef:
        name: buildah
        resolver: hub
      workspaces:
      - name: source
        workspace: source
      params:
      - name: IMAGE
        value: "quay.io/myorg/myapp:{{ revision }}"
      - name: DOCKERFILE
        value: "./Dockerfile"
  workspaces:
  - name: source
    emptyDir: {}
```

**Conditional Execution Based on File Changes:**

```yaml
# .tekton/docs-only.yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: docs-validation
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/on-path-change: "[docs/**, **.md]"
spec:
  pipelineSpec:
    tasks:
    - name: fetch-source
      taskRef:
        name: git-clone
        resolver: hub
      workspaces:
      - name: output
        workspace: source
      params:
      - name: url
        value: "{{ repo_url }}"
      - name: revision
        value: "{{ revision }}"
    - name: lint-docs
      runAfter: [fetch-source]
      taskRef:
        name: markdown-lint
        resolver: hub
      workspaces:
      - name: source
        workspace: source
  workspaces:
  - name: source
    emptyDir: {}
```

## Getting Started

### 5-Minute Quickstart

Get up and running with Pipelines-as-Code in just a few minutes:

1. **Install the CLI**:

   ```shell
   brew install tektoncd/pipelines-as-code/tkn-pac
   ```

2. **Bootstrap a new repository** (if you have a GitHub repo):

   ```console
   $ tkn pac bootstrap github
   ? Enter the Git repository url (default: https://github.com/owner/repo):
   ? Please enter your GitHub access token: ****
   ✓ Repository owner/repo has been created
   ✓ Repository has been configured
   ```

3. **Generate your first pipeline**:

   ```console
   $ cd your-repo
   $ tkn pac generate
   ? Enter the Git event type for triggering the pipeline: Pull Request
   ? Enter the target GIT branch for the Pull Request (default: main): main
   ✓ A basic template has been created in .tekton/pull-request.yaml
   ```

4. **Commit and push**:

   ```shell
   git add .tekton/
   git commit -m "Add Pipelines-as-Code configuration"
   git push
   ```

5. **Create a pull request** and watch your pipeline run automatically! 🎉

**Verification**: Check your repository's "Actions" or "Checks" tab to see the pipeline execution.

### Installation

#### Option 1: Homebrew (macOS/Linux)

```shell
brew install tektoncd/pipelines-as-code/tkn-pac
```

#### Option 2: Direct Download

```shell
# Download latest release
curl -L https://github.com/tektoncd/pipelines-as-code/releases/latest/download/tkn-pac-linux-amd64 -o tkn-pac
chmod +x tkn-pac
sudo mv tkn-pac /usr/local/bin/
```

#### Option 3: Install on Kubernetes Cluster

```shell
# Install Pipelines-as-Code controller
kubectl apply -f https://github.com/tektoncd/pipelines-as-code/releases/latest/download/release.yaml
```

**Verify Installation**:

```console
$ tkn pac version
Pipelines-as-Code version: v0.x.x
```

For detailed installation instructions including Windows, see the [official documentation](https://pipelinesascode.com/docs/install/).

### Creating your first repository

Once you have the `tkn-pac` CLI installed, you can set up your first repository with the `bootstrap` command. We have a full walk-through tutorial here:

<https://pipelinesascode.com/docs/install/getting-started/>

## Documentation

For more detailed information, please refer to the [official documentation](https://pipelinesascode.com).

The documentation for the development branch is available [here](https://docs.pipelinesascode.com/nightly).

## Contributing

We welcome contributions from everyone! Whether you're fixing bugs, adding features, improving documentation, or helping other users, your contributions make Pipelines-as-Code better.

### **Getting Started**

- Read our [development guide](https://pipelinesascode.com/docs/dev/) for setup instructions
- Check out [good first issues](https://github.com/tektoncd/pipelines-as-code/labels/good%20first%20issue) to get started
- Review our [Code of Conduct](code-of-conduct.md) to understand our community standards

### **How to Contribute**

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes and add tests
4. Run `make test` and `make lint` to verify your changes
5. Commit your changes (`git commit -m 'Add amazing feature'`)
6. Push to your branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

### **Contribution Types**

- **Code**: Bug fixes, new features, performance improvements
- **Documentation**: User guides, API docs, examples, blog posts
- **Testing**: Writing tests, improving test coverage, reporting bugs
- **Community**: Answering questions, mentoring new contributors, organizing events

## Community

Join our vibrant community of developers and DevOps engineers:

### **Getting Help**

- **GitHub Discussions**: Ask questions and get community support in [GitHub Discussions](https://github.com/tektoncd/pipelines-as-code/discussions)
- **Slack**: Join us on the TektonCD Slack in the [#pipelinesascode](https://tektoncd.slack.com/archives/C04URDDJ9MZ) channel ([Join TektonCD Slack](https://github.com/tektoncd/community/blob/main/contact.md#slack))
- **Issues**: Report bugs and request features via [GitHub Issues](https://github.com/tektoncd/pipelines-as-code/issues)

### **Contributing**

- **Good First Issues**: Start contributing with [good first issues](https://github.com/tektoncd/pipelines-as-code/labels/good%20first%20issue)
- **Help Wanted**: Check out [help wanted](https://github.com/tektoncd/pipelines-as-code/labels/help%20wanted) issues
- **Developer Docs**: See our [development guide](https://pipelinesascode.com/docs/dev/)

### **Stay Updated**

- **Releases**: Follow our [releases](https://github.com/tektoncd/pipelines-as-code/releases) for the latest updates

## License

This project is licensed under the [Apache 2.0 License](LICENSE).
