---
title: Overview
weight: 1
---

This page gives you a high-level view of the Pipelines-as-Code installation process and directs you to the right guide for your platform. Use it to plan your installation before diving into platform-specific steps.

Pipelines-as-Code supports different installation methods depending on your cluster platform and Git provider. The recommended approach is to configure Pipelines-as-Code with a [GitHub Application](https://docs.github.com/en/developers/apps/getting-started-with-apps/about-apps).

## Install Pipelines-as-Code Infrastructure

Installing Pipelines-as-Code involves two steps: deploying the controller to your cluster and connecting it to your Git provider. To get started:

* Install Pipelines-as-Code on your cluster:
  * **OpenShift**: If you run OpenShift Pipelines 1.7.x or later, Pipelines-as-Code is included automatically. See the [OpenShift installation guide]({{< relref "openshift" >}}) for details.
  * **Kubernetes**: Follow the [Kubernetes installation guide]({{< relref "kubernetes" >}}).
  * For manual installation on either platform, see the [manual installation guide]({{< relref "installation" >}}).
* Configure your Git provider (for example, a GitHub Application) to connect to Pipelines-as-Code.

## Git Provider Setup

After installing Pipelines-as-Code, configure your Git provider to send webhook events to the controller. If you have no preference, the GitHub Application method is recommended.

* [GitHub Application]({{< relref "/docs/providers/github-app" >}})
* [GitHub Webhook]({{< relref "/docs/providers/github-webhook" >}})
* [GitLab]({{< relref "/docs/providers/gitlab" >}})
* [Bitbucket Data Center]({{< relref "/docs/providers/bitbucket-datacenter" >}})
* [Bitbucket Cloud]({{< relref "/docs/providers/bitbucket-cloud" >}})
* [Forgejo]({{< relref "/docs/providers/forgejo" >}})
