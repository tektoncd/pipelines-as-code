---
title: "Development"
weight: 8
sidebar:
  open: true
---

This section covers how to contribute to Pipelines-as-Code (PAC), including development setup, architecture, testing, and the release process.

## What is Pipelines-as-Code?

Pipelines-as-Code is an opinionated CI/CD solution for Tekton that allows you to define and manage your pipelines directly from your source code repository. It integrates with multiple Git providers:

- GitHub (via GitHub App & Webhook)
- GitLab (Webhook)
- Forgejo (Webhook - Tech Preview)
- Bitbucket Cloud & Data Center (Webhook)

## Project Architecture

PAC consists of three main components:

- **Controller**: Processes Git webhook events, validates permissions, and creates PipelineRuns
- **Watcher**: Monitors PipelineRun status and reports back to Git providers
- **Webhook**: Receives webhook events from Git providers (runs as part of the controller)

See [Architecture]({{< relref "architecture" >}}) for detailed information.

## Ways to Contribute

### Code Contributions

- **Bug fixes**: Fix issues reported in GitHub
- **New features**: Add support for new providers, annotations, or workflows
- **Performance improvements**: Optimize controller logic, reduce memory usage
- **Refactoring**: Improve code quality and maintainability

### Documentation

- **User guides**: Help users understand features
- **API documentation**: Document CRs and configuration options
- **Examples**: Share real-world pipeline configurations
- **Blog posts**: Write about your PAC experience

### Testing

- **Write tests**: Improve test coverage for edge cases
- **Report bugs**: File detailed bug reports with reproduction steps
- **E2E testing**: Help test against different Git providers

### Community Support

- **Answer questions**: Help users on Slack or GitHub Discussions
- **Review PRs**: Provide feedback on pull requests
- **Mentor contributors**: Help onboard new contributors

## Code of Conduct

Before contributing, read the [Code of Conduct](https://github.com/tektoncd/pipelines-as-code/blob/main/code-of-conduct.md). The project is committed to providing a welcoming and inclusive environment for all contributors.

## Getting Started

1

Read the documentation

Familiarize yourself with PAC by reading the [user documentation](https://pipelinesascode.com/docs).

2

Set up your development environment

Follow the [Development Setup]({{< relref "setup" >}}) guide to configure your local environment.

3

Find an issue to work on

Browse [good first issues](https://github.com/tektoncd/pipelines-as-code/labels/good%20first%20issue) or [help wanted](https://github.com/tektoncd/pipelines-as-code/labels/help%20wanted) issues.

4

Submit your contribution

Create a pull request following our [contribution workflow](#contribution-workflow).

## Contribution Workflow

1. **Fork the repository** and clone it locally
2. **Create a feature branch** from `main`:

   ```bash
   git checkout -b feature/amazing-feature
   ```

3. **Make your changes** and add tests
4. **Run quality checks**:

   ```bash
   make test lint
   ```

5. **Commit your changes** following [conventional commits](https://www.conventionalcommits.org/)
6. **Push to your fork**:

   ```bash
   git push origin feature/amazing-feature
   ```

7. **Open a pull request** with a clear description

## Development Tools

The project uses several tools to maintain code quality:

- **golangci-lint**: Go code linting
- **yamllint**: YAML file validation
- **markdownlint**: Markdown documentation linting
- **ruff**: Python code formatting and linting
- **shellcheck**: Shell script validation
- **vale**: Grammar and style checking for docs
- **codespell**: Spell checking
- **pre-commit**: Git hooks to catch issues before pushing

See [Development Setup]({{< relref "setup" >}}) for installation instructions.

## Testing Requirements

All code contributions must include appropriate tests. PAC uses `gotest.tools/v3` for unit tests (never testify).

### Test Types

- **Unit tests**: Test individual functions and packages

  ```bash
  make test
  ```

- **E2E tests**: Test full workflows against real Git providers

  ```bash
  make test-e2e
  ```

- **Coverage reports**: Generate HTML coverage reports

  ```bash
  make html-coverage
  ```

See [Testing Guide]({{< relref "testing" >}}) for detailed information.

## AI Assistance Disclosure

When submitting pull requests, you must disclose any AI/LLM assistance used during development. This promotes transparency and proper attribution.

If you used AI assistance:

1. Check the appropriate boxes in the PR template’s ”🤖 AI Assistance” section
2. Specify which LLM was used (GitHub Copilot, ChatGPT, Claude, etc.)
3. Indicate the extent of assistance (code generation, documentation, etc.)
4. Add `Co-authored-by` trailers to commit messages when AI significantly contributed:

```bash
./hack/add-llm-coauthor.sh
```

Example commit trailer:

```text
Co-authored-by: Copilot <Copilot@users.noreply.github.com>
```

## Target Architecture

PAC targets both **arm64** and **amd64** architectures. When contributing:

- Ensure Docker images support both architectures
- Test on arm64 when possible (dogfooding runs on arm64)
- Use multi-arch base images in Dockerfiles

## Communication Channels

### Getting Help

- **GitHub Discussions**: [Ask questions](https://github.com/tektoncd/pipelines-as-code/discussions)
- **Slack**: Join [#pipelinesascode](https://tektoncd.slack.com/archives/C04URDDJ9MZ) on TektonCD Slack
- **Issues**: [Report bugs](https://github.com/tektoncd/pipelines-as-code/issues)

### Staying Updated

- **Releases**: Follow [GitHub releases](https://github.com/tektoncd/pipelines-as-code/releases)
- **Dev docs**: Check [nightly docs](https://docs.pipelinesascode.com/nightly) for latest changes

## Next Steps

{{< cards >}}
  {{< card link="setup" title="Development Setup" subtitle="Set up your local development environment" >}}
  {{< card link="architecture" title="Architecture" subtitle="Understand the PAC architecture and components" >}}
  {{< card link="testing" title="Testing Guide" subtitle="Learn how to write and run tests" >}}
  {{< card link="release-process" title="Release Process" subtitle="Understand how releases are created" >}}
{{< /cards >}}
