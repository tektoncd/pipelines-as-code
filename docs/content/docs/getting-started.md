---
title: Getting Started Guide
weight: 2
---

This guide walks you through the end-to-end setup of Pipelines-as-Code: installing it on your cluster, creating a GitHub Application, configuring a Repository CR, and testing the setup with a pull request. By the end, you will have a working CI pipeline that runs automatically on every PR.

## Prerequisites

Before you begin, make sure you have the following in place:

- A Kubernetes cluster like [Kind](https://kind.sigs.k8s.io/), [Minikube](https://minikube.sigs.k8s.io/docs/start/), or [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview)
- The OpenShift Pipelines Operator installed or [Tekton Pipelines](https://tekton.dev/docs/pipelines/install/) installed on your cluster
- The [Tekton CLI](https://tekton.dev/docs/cli/#installation) installed, and the Pipelines-as-Code CLI plugin ([tkn-pac]({{< relref "/docs/cli/installation" >}})) installed

## Installing Pipelines-as-Code

You need Pipelines-as-Code running on your cluster before you can connect it to a Git repository. There are multiple ways to install it. The easiest way is to have it managed by the OpenShift Pipelines Operator, which installs Pipelines-as-Code by default.

You can also install it manually by applying the YAML files to your cluster or use `tkn pac bootstrap` to do it for you. The `tkn pac bootstrap` command is the fastest way to get started for development and testing.

If you are running Pipelines-as-Code in production, consider installing using a GitOps tool like [OpenShift GitOps](https://www.openshift.com/learn/topics/gitops/) or [ArgoCD](https://argoproj.github.io/argo-cd/) following the manual installation instructions.

This guide uses `tkn pac bootstrap` to get you started.

{{< callout type="info" >}}
This assumes you are using the public GitHub instance. If you are using GitHub
Enterprise, add the `--github-hostname` flag with the hostname of your GitHub Enterprise instance.
{{< /callout >}}

### Pipelines-as-Code installation

When you run `tkn pac bootstrap`, it first checks whether Pipelines-as-Code is already installed on your cluster. If it is not, you are prompted to install it.

```bash
% tkn pac bootstrap
=> Checking if Pipelines-as-Code is installed.
🕵️ Pipelines-as-Code doesn't seem to be installed in the pipelines-as-code namespace.
? Do you want me to install Pipelines-as-Code v0.19.2? (Y/n)
```

Press `Y` to install it.

### Webhook forwarder

Your Git provider needs to reach the Pipelines-as-Code controller over the internet to deliver webhook events. If your cluster is not publicly accessible, you need a forwarder.

The next question asks whether to install the webhook forwarding tool [gosmee](https://github.com/chmouel/gosmee). This prompt only appears if you are not running on OpenShift. Installing gosmee is optional, but it is the most straightforward way to make the controller reachable by GitHub.

{{< callout type="warning" >}}
gosmee is intended for development and testing only, not for production use.
{{< /callout >}}

```console
Pipelines-as-Code does not install an Ingress object to allow the controller to be accessed from the internet.
We can install a webhook forwarder called gosmee (https://github.com/chmouel/gosmee) using a https://hook.pipelinesascode.com URL.
This will let your git platform provider (e.g., GitHub) reach the controller without requiring public access.
? Do you want me to install the gosmee forwarder? (Y/n)
💡 Your gosmee forward URL has been generated: https://hook.pipelinesascode.com/zZVuUUOkCzPD
```

### Tekton Dashboard

Having a dashboard lets you view PipelineRun logs and details directly from links that Pipelines-as-Code posts on your pull requests.

If you have the [Tekton Dashboard](https://github.com/tektoncd/dashboard) installed, Pipelines-as-Code can use it to link to PipelineRun logs and details. On OpenShift, it automatically detects the OpenShift console Route.

```console
👀 We have detected a tekton dashboard install on http://dashboard.paac-127-0-0-1.nip.io
? Do you want me to use it? Yes
```

## GitHub Application

A GitHub Application is how Pipelines-as-Code authenticates with GitHub, receives webhook events, and posts status checks on your pull requests. While Pipelines-as-Code also supports a simple webhook, a GitHub Application provides the best integration experience.

Enter the name of the GitHub Application you want to create. This name must be unique across GitHub, so choose a name that does not conflict with existing applications.

```console
? Enter the name of your GitHub application: My PAAC Application
```

After you press Enter, the CLI opens your web browser to `https://localhost:8080`, which displays a button to create the GitHub Application. Click the button to be redirected to GitHub, where you see the following screen:

![GitHub Application Creation](/images/github-app-creation-screen.png)

Click the "Create GitHub App" button (or change the name first if needed). Return to the terminal where `tkn pac bootstrap` is running. The output confirms that the GitHub Application has been created and a secret token has been generated.

```console
🔑 Secret pipelines-as-code-secret has been created in the pipelines-as-code namespace
🚀 You can now add your newly created application to your repository by going to this URL:

https://github.com/apps/my-paac-application

💡 Don't forget to run "tkn pac create repository" to create a new Repository CR on your cluster.
```

Visit the newly created GitHub Application by opening the URL shown in the output. Click the "App settings" button on that page to inspect the GitHub Application configuration. The `tkn pac bootstrap` command configures all required settings, but you can adjust advanced options there if necessary.

{{< callout type="info" >}}
The "Advanced" tab shows all recent deliveries from the GitHub App to the Pipelines-as-Code controller. Use it to debug communication issues between GitHub and the endpoint URL, or to investigate which events are being sent.
{{< /callout >}}

### Creating a GitHub repository

Before you can create a Repository CR, you need a Git repository for Pipelines-as-Code to watch. If you already have one, skip to the next section.

Create a repository using this template:

<https://github.com/pipelines-as-code/pac-demo/generate>

Choose your username (e.g., `chmouel`) and a repository name (e.g., `pac-demo`), then click the "Create repository from template" button.

{{< callout type="info" >}}
Pipelines-as-Code also works with [private repositories]({{< relref "/docs/advanced/private-repositories" >}}). For simplicity, this guide uses a public repository.
{{< /callout >}}

Your repository is now created on GitHub at <https://github.com/yourusername/pac-demo>.

### Install the GitHub Application on your repository

The GitHub Application must be installed on the specific repository you want Pipelines-as-Code to manage. This grants the application permission to receive events and post status checks.

Add the GitHub Application to your GitHub repository:

1. Go to the GitHub App URL provided by `tkn pac bootstrap`, for example:
   [https://github.com/apps/my-paac-application](https://github.com/apps/my-paac-application).
2. Click on the "Install" button.
3. Choose the repository you just created under your username.

The installation screen looks similar to this:

![GitHub Application Installation](/images/github-app-install-application-on-repo.png)

### Clone the newly created GitHub repository

You need a local clone so the `tkn pac` CLI can read Git metadata and provide helpful defaults for subsequent commands. Clone the repository and change into its directory:

```bash
git clone https://github.com/$yourusername/pac-demo
cd pac-demo
```

## Create a Repository CR

The Repository CR is the primary configuration object for Pipelines-as-Code. It tells Pipelines-as-Code which Git repository to watch, which namespace to run PipelineRuns in, and how to authenticate with the Git provider. Without a Repository CR, Pipelines-as-Code has no way to connect incoming webhook events to your cluster.

{{< callout type="info" >}}
For a full reference of all Repository CR fields, see the [Repository CR guide]({{< relref "/docs/guides/repository-crd" >}}).
{{< /callout >}}

Create a Repository CR with the command:

```bash
tkn pac create repository
```

The command detects the Git information of the current repository and lets you confirm or change the values.

```console
? Enter the Git repository url (default: https://github.com/chmouel/pac-demo):
```

Press Enter to accept the default value. The next prompt asks which namespace to use for running your CI. Each Repository CR runs PipelineRuns in its own namespace, isolating workloads. Accept the default or enter a different namespace:

```console
? Please enter the namespace where the pipeline should run (default: pac-demo-pipelines):
! Namespace pac-demo-pipelines is not found
? Would you like me to create the namespace pac-demo-pipelines? (Y/n)
```

After confirmation, the command creates the Repository CR in your cluster and generates a `.tekton/` directory containing a `pipelinerun.yaml` file.

```console
ℹ Directory .tekton has been created.
✓ We have detected your repository using the programming language Go.
✓ A basic template has been created in /tmp/pac-demo/.tekton/pipelinerun.yaml, feel free to customize it.
ℹ You can test your pipeline by pushing the generated template to your git repository
```

The `tkn pac create repository` command detects the programming language of the repository and generates a tailored template. For a Go repository, it adds the [golangci-lint](https://artifacthub.io/packages/tekton-task/tekton-catalog-tasks/golangci-lint) linter as a task to the PipelineRun.

Open the file `.tekton/pipelinerun.yaml` to inspect its contents. The file includes comments explaining how it works.

## Creating a pull request

With the Repository CR created and `.tekton/pipelinerun.yaml` generated, you can now test the full integration. When you open a pull request, Pipelines-as-Code detects the event, finds matching pipeline definitions in the `.tekton/` directory, and runs them automatically.

First, create a branch for the pull request.

```bash
git checkout -b tektonci
```

Commit the `.tekton/pipelinerun.yaml` file and push the branch to the repository:

```bash
git add .
git commit -m "Adding Tekton CI"
git push origin tektonci
```

{{< callout type="info" >}}
This assumes your system is configured to push to GitHub. If not, see the official GitHub documentation: <https://docs.github.com/en/get-started/getting-started-with-git/setting-your-username-in-git>
{{< /callout >}}

After pushing the branch, create a new pull request by visiting the following URL (replace `yourusername` with your GitHub username):
<https://github.com/yourusername/pac-demo/pull/new/tektonci>

Once you create the pull request, Pipelines-as-Code triggers a PipelineRun on it:

![CI pipeline triggered on pull request](/images/github-app-install-CI-triggered.png)

Click the "Details" link to view the PipelineRun status. Pipelines-as-Code provides links to follow the logs on the [Tekton Dashboard](https://github.com/tektoncd/dashboard), the OpenShift Pipelines [Console](https://docs.openshift.com/container-platform/latest/web_console/web-console.html), or through the [Tekton CLI](https://tekton.dev/docs/cli/) directly on your cluster.

When the PipelineRun finishes, the Details screen shows an error:

![CI pipeline failed with linting errors](/images/github-app-install-CI-failed.png)

This failure is expected. The demo repository contains linting errors in the Go code that golangci-lint flags as incorrect. Notice how the error links to the specific line of code. Pipelines-as-Code analyzes PipelineRun log errors and matches them to the corresponding source lines so you can quickly identify and fix issues.

![Matching annotations linking errors to code lines](/images/github-app-matching-annotations.png)

### Fixing the error and pushing again

Pipelines-as-Code automatically triggers a new PipelineRun whenever you push a new commit to an open pull request. This means you can fix the error, push again, and see the result without manually re-running anything.

Return to the terminal to fix the error. Edit the file `main.go`, select everything, and replace it with this content:

```go
package main

import (
    "fmt"
    "os"
)

func main() {
    fmt.Fprintf(os.Stdout, "Hello world")
}
```

Commit this file and push it to your branch:

```bash
git commit -a -m "Errare humanum est, ignoscere divinum."
git push origin tektonci
```

Back in the browser, Pipelines-as-Code has triggered a new PipelineRun, and this time it succeeds:

![CI pipeline succeeded after fix](/images/github-app-install-CI-succeeded.png)

## Conclusion

You have successfully set up Pipelines-as-Code for running continuous integration on your repository. You can now [customize]({{< relref "/docs/guides/creating-pipelines" >}}) the `.tekton/pipelinerun.yaml` file and add additional tasks as needed.

### Summary

This guide covered:

- [Installing]({{< relref "/docs/installation/installation" >}}) Pipelines-as-Code on a Kubernetes cluster
- Creating a [GitHub Application]({{< relref "/docs/providers/github-app" >}})
- Creating a [Repository CR]({{< relref "/docs/guides/repository-crd" >}})
- Generating a Pipelines-as-Code [PipelineRun]({{< relref "/docs/guides/creating-pipelines" >}})
- [Running the PipelineRun]({{< relref "/docs/guides/running-pipelines" >}}) on a pull request
