# Tekton Pipelines-as-Code

[![Latest Release](https://img.shields.io/github/v/release/tektoncd/pipelines-as-code)](https://github.com/tektoncd/pipelines-as-code/releases/latest)
[![Container Repository on GHCR](https://img.shields.io/badge/GHCR-image-87DCC0.svg?logo=GitHub)](https://github.com/orgs/tektoncd/packages/container/package/pipelines-as-code%2Fpipelines-as-code-controller)
[![Go Report Card](https://goreportcard.com/badge/tektoncd/pipelines-as-code)](https://goreportcard.com/report/tektoncd/pipelines-as-code)
[![E2E Tests](https://github.com/tektoncd/pipelines-as-code/actions/workflows/e2e.yaml/badge.svg)](https://github.com/tektoncd/pipelines-as-code/actions/workflows/e2e.yaml)
[![License](https://img.shields.io/github/license/tektoncd/pipelines-as-code)](LICENSE)

<img src="docs/static/images/pac-logo-with-tagline-small.png" alt="PAC LOGO" width="300" align="right"/>

Pipelines-as-Code is an opinionated CI/CD framework for Tekton that lets you
define and run pipelines directly from your Git repository. Store your
`.tekton/` PipelineRuns alongside your source code, trigger them on Git events,
and get results reported back as pull request checks.

## Features

- **Git-native**: pipelines live in `.tekton/` and are versioned with your code
- **Multi-provider**: GitHub Apps & Webhooks, GitLab, Bitbucket Cloud &
  Data Center, Forgejo
- **ChatOps**: `/test`, `/retest`, `/cancel` and `[skip ci]` from PR comments
- **Inline resolver**: bundles remote tasks from Artifact Hub before cluster submission
- **Automated housekeeping**: prune old PipelineRuns and cancel superseded runs
  on new pushes

## Read the docs

Full documentation is at **<https://pipelinesascode.com>**

### Installation

- [Overview](https://pipelinesascode.com/docs/installation/overview/)
- [Getting started](https://pipelinesascode.com/docs/getting-started/)

### Guides

- [Creating pipelines](https://pipelinesascode.com/docs/guides/creating-pipelines/)
- [Running pipelines](https://pipelinesascode.com/docs/guides/running-pipelines/)
- [Pipeline resolution](https://pipelinesascode.com/docs/guides/pipeline-resolution/)
- [Repository CRD](https://pipelinesascode.com/docs/guides/repository-crd/)
- [GitOps commands](https://pipelinesascode.com/docs/guides/gitops-commands/)
- [Statuses & checks](https://pipelinesascode.com/docs/guides/statuses/)

### Advanced

- [Cleanup](https://pipelinesascode.com/docs/advanced/cleanup/)
- [Incoming webhooks](https://pipelinesascode.com/docs/advanced/incoming-webhooks/)

### Git providers

- [GitHub App](https://pipelinesascode.com/docs/providers/github-app/)
- [GitHub Webhook](https://pipelinesascode.com/docs/providers/github-webhook/)
- [GitLab](https://pipelinesascode.com/docs/providers/gitlab/)
- [Bitbucket Cloud](https://pipelinesascode.com/docs/providers/bitbucket-cloud/)
- [Bitbucket Data Center](https://pipelinesascode.com/docs/providers/bitbucket-datacenter/)
- [Forgejo](https://pipelinesascode.com/docs/providers/forgejo/)

### API reference

- [Repository CRD](https://pipelinesascode.com/docs/api/repository/)
- [ConfigMap settings](https://pipelinesascode.com/docs/api/configmap/)

### CLI reference

- [`tkn pac`](https://pipelinesascode.com/docs/cli/)

### Operations

- [Configuration](https://pipelinesascode.com/docs/operations/configuration/)

### Development

- [Developer guide](https://pipelinesascode.com/docs/dev/)

## Want to start using Pipelines-as-Code?

Install the CLI and bootstrap your first repository:

```shell
brew install --cask openshift-pipelines/pipelines-as-code/tektoncd-pac
tkn pac bootstrap
```

Then follow the [getting started tutorial](https://pipelinesascode.com/docs/getting-started/).

Releases: <https://github.com/tektoncd/pipelines-as-code/releases>

## Contributing

- See the [code-of-conduct.md](code-of-conduct.md)
- Read the [development guide](https://pipelinesascode.com/docs/dev/)
- Browse [good first issues](https://github.com/tektoncd/pipelines-as-code/labels/good%20first%20issue) or [help wanted](https://github.com/tektoncd/pipelines-as-code/labels/help%20wanted)

## Community / Getting help

- **Slack**: [#pipelinesascode](https://tektoncd.slack.com/archives/C04URDDJ9MZ)
  ([join TektonCD Slack](https://github.com/tektoncd/community/blob/main/contact.md#slack))
- **GitHub Discussions**: <https://github.com/tektoncd/pipelines-as-code/discussions>
- **GitHub Issues**: <https://github.com/tektoncd/pipelines-as-code/issues>

## License

[Apache 2.0](LICENSE)
