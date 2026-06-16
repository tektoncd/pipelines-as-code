---
title: Pipelines as Code
toc: false
hero_subtitle: "An opinionated CI based on Tekton."
---

Pipelines-as-Code lets you define your CI/CD using
[Tekton](https://tekton.dev) PipelineRuns and Tasks in files stored directly
alongside your source code. Your pipeline definitions are versioned, reviewed,
and collaborated on just like your application code — bringing the full power of
the Git workflow to your CI/CD pipelines. With a single pane of glass from your
SCM provider (GitHub, GitLab, Bitbucket, or Forgejo), PipelineRuns are
automatically triggered for Pull Requests and Push events.

{{< cards >}}
  {{< card link="docs/getting-started" title="Getting Started" subtitle="Set up your first pipeline in minutes" >}}
  {{< card link="docs/concepts" title="Concepts" subtitle="Understand the architecture and key components" >}}
  {{< card link="docs/guides" title="Guides" subtitle="Learn how to author and run pipelines" >}}
  {{< card link="docs/providers" title="Git Providers" subtitle="GitHub, GitLab, Bitbucket, Forgejo" >}}
  {{< card link="docs/cli" title="CLI Reference" subtitle="tkn-pac commands" >}}
  {{< card link="docs/api" title="API Reference" subtitle="Repository CR, ConfigMap, and settings" >}}
{{< /cards >}}

## Features

{{< cards >}}
  {{< feature-card title="PR Status & Checks" subtitle="Pull-request status support with GitHub Checks API, including rechecks for Pull Request and Push events." popup="When you open or update a pull request, Pipelines as Code shows status checks (like pass/fail) directly on the PR, so you can see at a glance whether your pipeline succeeded. It can re-run these checks automatically when you push new commits or update the PR." >}}
  {{< feature-card title="GitOps Commands" subtitle="Pull-request actions through comments — `/retest`, `/test`, `/cancel`, and more." popup="You can control pipelines directly from PR comments: type `/retest` to re-run failed checks, `/test` to run a specific pipeline, `/cancel` to stop a run, and more—no need to leave the PR page or use separate tools." >}}
  {{< feature-card title="Automatic Resolution" subtitle="Automatic Task resolution from local files, Artifact Hub, and remote URLs." popup="Pipelines as Code can automatically find and use your pipeline definitions from your repository, from Artifact Hub, or from a remote URL—so you don't have to paste long YAML files into a UI; it discovers and runs the right pipeline automatically." >}}
  {{< feature-card title="Multi-Provider Support" subtitle="GitHub App, GitHub Webhook, GitLab, Bitbucket Cloud, Bitbucket Data Center, and Forgejo." popup="Works with the Git platform you already use: GitHub (App or Webhook), GitLab, Bitbucket Cloud, Bitbucket Data Center, and Forgejo—so you can keep your existing workflow and still get all the benefits of Pipelines as Code." >}}
  {{< feature-card title="Event Filtering" subtitle="Git event filtering with separate pipelines for each event type." popup="You can run different pipelines for different events (for example, only on push to main, or only on pull requests), so your CI stays fast and relevant to what actually changed in your code." >}}
  {{< feature-card title="CLI Tooling" subtitle="`tkn-pac` CLI plugin for bootstrapping, managing repos, and debugging." popup="The `tkn-pac` command-line tool helps you set up new repositories, manage Pipelines as Code configurations, and debug pipeline runs—all from your terminal without needing to navigate web interfaces." >}}
{{< /cards >}}

## Getting Started

The easiest way to get started is to use `tkn pac bootstrap` on a [Kind](https://kind.sigs.k8s.io/) cluster or [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview) environment. See the [Getting Started guide]({{< relref "/docs/getting-started" >}}) for
step-by-step instructions.
