---
title: GitHub Apps
weight: 1
---

This page covers how to configure Pipelines-as-Code with a GitHub App. Use this method when you want the richest integration with GitHub, including the CheckRun API, GitOps comments, and automatic token management. A GitHub App is the recommended approach for most GitHub users, and you typically need only one per cluster.

## Prerequisites

- A running Pipelines-as-Code [installation]({{< relref "/docs/installation/installation" >}})
- Admin access to a GitHub account or organization
- The public URL of your Pipelines-as-Code controller route or ingress endpoint

## Setup using the CLI

Use the [`tkn pac bootstrap`]({{< relref "/docs/cli" >}}) command to create a GitHub App, configure it with your Git repository, and create the required secrets automatically. After creating the GitHub App, install it on the repositories you want to use with Pipelines-as-Code.

If you prefer to configure everything by hand, follow the [manual setup](#manual-setup) steps below.

## Manual Setup

To create the GitHub App manually:

- Go to <https://github.com/settings/apps> (or *Settings > Developer settings > GitHub Apps*) and click on the **New GitHub
  App** button
- Provide the following information in the GitHub App form:
  - **GitHub Application Name**: `OpenShift Pipelines`
  - **Homepage URL**: *[OpenShift Console URL]*
  - **Webhook URL**: *[the Pipelines-as-Code route or ingress URL as copied in the previous section]*
  - **Webhook secret**: *[an arbitrary secret, you can generate one with `head -c 30 /dev/random | base64`]*

- Select the following repository permissions:
  - **Checks**: `Read & Write`
  - **Contents**: `Read & Write`
  - **Issues**: `Read & Write`
  - **Metadata**: `Readonly`
  - **Pull request**: `Read & Write`

- Select the following organization permissions:
  - **Members**: `Readonly`

- Subscribe to following events:
  - Check run
  - Check suite
  - Commit comment
  - Issue comment
  - Pull request
  - Push

{{< callout type="info" >}}
> You can see a screenshot of how the GitHub App permissions look like [here](https://user-images.githubusercontent.com/98980/124132813-7e53f580-da81-11eb-9eb4-e4f1487cf7a0.png)
{{< /callout >}}

- Click on **Create GitHub App**.

- Take note of the **App ID** at the top of the page on the **General** tab, under **About**, for the GitHub App you just created.

- In the **Private keys** section, click on **Generate Private key** to generate a private key for the GitHub app. It downloads automatically. Store the private key in a safe place because you need it in the next section and when reconfiguring this app for a different cluster.

### Configure Pipelines-as-Code to access the GitHub App

Pipelines-as-Code needs a Kubernetes secret containing the GitHub App private key and the webhook secret. This secret lets the controller [generate tokens](https://docs.github.com/en/developers/apps/building-github-apps/identifying-and-authorizing-users-for-github-apps) on behalf of the user who triggered the event and validate incoming webhook payloads.

Run the following command, replacing the placeholder values:

- `APP_ID` with the GitHub App **App ID** copied in the previous section
- `WEBHOOK_SECRET` with the webhook secret provided when you created the GitHub App
- `PATH_PRIVATE_KEY` with the path to the private key that was downloaded in the
  previous section

```bash
kubectl -n pipelines-as-code create secret generic pipelines-as-code-secret \
        --from-literal github-private-key="$(cat $PATH_PRIVATE_KEY)" \
        --from-literal github-application-id="APP_ID" \
        --from-literal webhook.secret="WEBHOOK_SECRET"
```

Finally, install the App on the repositories you want to use with Pipelines-as-Code.

## Notes

- Pipelines-as-Code supports GitHub Enterprise with no special configuration required. Pipelines-as-Code automatically detects the header set by GitHub Enterprise and uses the GitHub Enterprise API auth URL instead of the public GitHub API.
