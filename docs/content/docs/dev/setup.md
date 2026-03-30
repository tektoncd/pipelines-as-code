---
title: "Development Setup"
weight: 1
---

This guide walks you through setting up a complete development environment for Pipelines-as-Code, from initial setup to running your first test.

## Prerequisites

Before you begin, ensure you have the following installed:

### Required Tools

- **Go 1.20+**: [Download](https://go.dev/dl/)
- **kubectl**: [Installation guide](https://kubernetes.io/docs/tasks/tools/)
- **Docker or Podman**: For building container images
- **Git**: For version control

### Recommended Tools

- **kind**: [Installation guide](https://kind.sigs.k8s.io/docs/user/quick-start/) - For local Kubernetes clusters
- **ko**: [Installation guide](https://ko.build/install/) - For building and deploying Go container images
- **tkn**: [Installation guide](https://tekton.dev/docs/cli/) - Tekton CLI

## Local Development with startpaac

The recommended way to set up a local development environment is using [startpaac](https://github.com/pipelines-as-code/startpaac), which provides an interactive, modular setup.

### What startpaac Provides

- Kind cluster with local container registry
- Nginx ingress controller for webhook routing
- Tekton Pipelines and Dashboard
- Pipelines-as-Code controller, watcher, and webhook
- Forgejo instance for local E2E testing

### Quick Start

1

Clone startpaac

```bash
git clone https://github.com/pipelines-as-code/startpaac
cd startpaac
```

2

Run the setup

```bash
./startpaac -a
```

This will install all components. You can also run individual modules:

```bash
# Install only specific components
./startpaac -k  # Kind cluster only
./startpaac -t  # Tekton only
./startpaac -p  # PAC only
./startpaac -f  # Forgejo only
```

3

Verify the installation

```bash
kubectl get pods -n pipelines-as-code
```

You should see the controller, watcher, and webhook pods running.

See the [startpaac README](https://github.com/pipelines-as-code/startpaac) for detailed configuration options and environment variables.

## Manual Development Setup

If you prefer manual setup or need more control:

1

Fork and clone the repository

```bash
git clone https://github.com/<your-username>/pipelines-as-code.git
cd pipelines-as-code
```

2

Install development dependencies

```bash
# Install pre-commit hooks
brew install pre-commit  # macOS
# or
sudo apt install pre-commit  # Ubuntu/Debian
# or
pip install pre-commit  # Using pip

# Install pre-commit hooks
pre-commit install
```

3

Install linting and formatting tools

```bash
# Go tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install mvdan.cc/gofumpt@latest

# YAML linter
pip install yamllint

# Markdown linter
npm install -g markdownlint-cli

# Python formatter
pip install ruff

# Shell script linter
brew install shellcheck  # macOS
sudo apt install shellcheck  # Ubuntu/Debian

# Documentation grammar checker
brew install vale  # macOS
# or download from https://github.com/errata-ai/vale/releases

# Spell checker
pip install codespell
```

4

Set up Go dependencies

```bash
# Download dependencies and create vendor directory
go mod download
make vendor
```

5

Build the binaries

```bash
# Build all binaries
make allbinaries

# Or build individual components
make bin/pipelines-as-code-controller
make bin/pipelines-as-code-watcher
make bin/tkn-pac
```

## Deploying Changes to Kubernetes

### Using ko (Recommended)

`ko` allows you to rapidly build and deploy Go applications to Kubernetes:

1

Set the Docker repository

Point `ko` to your local registry (if using kind with startpaac):

```bash
export KO_DOCKER_REPO=localhost:5000
```

Or use your own registry:

```bash
export KO_DOCKER_REPO=quay.io/<your-username>
```

2

Deploy to Kubernetes

```bash
# Deploy all PAC components
ko apply -f config -B
```

This builds the container images and deploys them to your cluster.

3

Verify the deployment

```bash
kubectl get pods -n pipelines-as-code
kubectl logs -n pipelines-as-code -l app.kubernetes.io/name=controller -f
```

### Iterative Development

When making code changes:

1. Edit your Go files
2. Run formatting:

   ```bash
   make fumpt
   ```

3. Redeploy with ko:

   ```bash
   env KO_DOCKER_REPO=localhost:5000 ko apply -f config -B
   ```

4. Watch the logs:

   ```bash
   kubectl logs -n pipelines-as-code -l app.kubernetes.io/name=controller -f
   ```

## Code Quality Workflow

### After Editing Code

- Go Files
- Python Files
- Markdown Files

```bash
make fumpt
```

This formats Go code using `gofumpt` (a stricter version of `gofmt`).

```bash
make fix-python-errors
```

This formats and fixes Python code using `ruff`.

```bash
make fix-markdownlint && make fix-trailing-spaces
```

This fixes markdown linting issues and removes trailing spaces.

### Before Committing

1

Run tests

```bash
make test
```

2

Run linters

```bash
make lint
```

3

Or run both together

```bash
make check
```

## Pre-commit Hooks

Pre-commit hooks automatically run quality checks before you push code.

### Installation

```bash
pre-commit install
```

### What Gets Checked

The pre-commit hooks run:

- **golangci-lint**: Go code quality
- **yamllint**: YAML syntax and style
- **markdownlint**: Markdown formatting
- **ruff**: Python code formatting
- **shellcheck**: Shell script validation
- **vale**: Grammar checking for documentation
- **codespell**: Spell checking

### Skipping Hooks

Only skip hooks when absolutely necessary. Pre-commit checks help maintain code quality.

```bash
# Skip all hooks
git push --no-verify

# Skip a specific hook
SKIP=lint-md git push
```

## Working with Dependencies

### Adding a New Dependency

1

Add the dependency

```bash
go get -u github.com/example/dependency
```

2

Update vendor directory

```bash
make vendor
```

Always run `make vendor` after adding or updating dependencies. This is required!

3

Verify the build

```bash
make allbinaries test
```

### Updating All Dependencies

```bash
go get -u ./...
make vendor
```

See the [developer documentation]({{< relref "." >}}) for additional instructions on updating dependencies and handling version conflicts.

## Documentation Preview

PAC uses Hugo for documentation. To preview documentation changes locally:

1

Start the Hugo server

```bash
make dev-docs
```

This downloads Hugo and starts a live-reload server.

2

View the documentation

Open <http://localhost:1313> in your browser. Changes to documentation files automatically reload the page.

## Debugging

### Debugging the Controller

1

Create a webhook forwarding URL

Generate a hook URL at <https://hook.pipelinesascode.com/new>

2

Forward webhooks to your local controller

Use [gosmee](https://github.com/chmouel/gosmee) to forward webhook events:

```bash
gosmee client https://hook.pipelinesascode.com/YOUR_ID http://localhost:8080
```

3

Save webhook replays (optional)

```bash
gosmee client --saveDir /tmp/replays https://hook.pipelinesascode.com/YOUR_ID http://localhost:8080
```

This saves each webhook to `/tmp/replays` as a shell script you can replay.

### Watching Logs with snazy

[snazy](https://github.com/chmouel/snazy) makes JSON logs more readable:

![snazy screenshot](/images/pac-snazy.png)

```bash
kubectl logs -n pipelines-as-code -l app.kubernetes.io/name=controller -f | snazy
```

## Common Make Targets

```bash
make help              # Show all available targets
make all               # Build, test, and lint everything
make allbinaries       # Build all binaries
make test              # Run unit tests
make test-no-cache     # Run tests without cache
make test-e2e          # Run E2E tests
make lint              # Run all linters
make fix-linters       # Auto-fix most linting issues
make vendor            # Update vendor directory
make clean             # Clean build artifacts
make html-coverage     # Generate HTML test coverage report
make update-golden     # Update golden test files
make dev-docs          # Preview documentation locally
```

## Troubleshooting

### Build Issues

**Problem**: `vendor/` directory out of sync
**Solution**: Run `make vendor` to regenerate it.

### Test Issues

**Problem**: Tests failing due to stale golden files
**Solution**: Regenerate golden files with `make update-golden`.

### Deployment Issues

**Problem**: Changes not reflected in the cluster
**Solution**: Ensure you’re using the correct `KO_DOCKER_REPO` and the pods have restarted:

```bash
kubectl delete pods -n pipelines-as-code -l app.kubernetes.io/name=controller
```

## Next Steps

{{< cards >}}
  {{< card link="../testing" title="Testing Guide" subtitle="Learn how to write and run tests" >}}
  {{< card link="../architecture" title="Architecture" subtitle="Understand the PAC architecture" >}}
  {{< card link="../flows-diagram" title="Event Flows" subtitle="See how events flow through the system" >}}
  {{< card link="../release-process" title="Release Process" subtitle="Learn about the release process" >}}
{{< /cards >}}
