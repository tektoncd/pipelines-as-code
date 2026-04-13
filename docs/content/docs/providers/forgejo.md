---
title: Forgejo
weight: 6
---

{{< tech_preview "Forgejo" >}}

This page covers how to configure Pipelines-as-Code with Forgejo through a webhook. Use this method to run Tekton pipelines triggered by pull requests and push events on a self-hosted Forgejo instance. Forgejo is a community-driven Git forge that originated as a fork of Gitea, and Pipelines-as-Code supports it as a first-class provider type.

## Prerequisites

- A running Pipelines-as-Code [installation]({{< relref "/docs/installation/installation" >}})
- A Forgejo personal access token (see below)
- The public URL of your Pipelines-as-Code controller route or ingress endpoint

## Create a Forgejo Personal Access Token

Create a Forgejo token by going to the Applications tab
of the user settings, or to this URL (replace the domain name with your domain
name):

<https://your.forgejo.domain/user/settings/applications>

When creating the token, select these scopes:

### Required Scopes

These scopes are necessary for basic Pipelines-as-Code functionality:

- **Repository** (Write) - For setting commit status and reading repository contents
- **Issue** (Write) - For creating and editing comments on pull requests

### Optional Scopes

- **Organization** (Read) - Only required if using [team-based policies]({{< relref "/docs/advanced/policy-authorization" >}}) to restrict pipeline triggers based on Forgejo organization team membership

{{< callout type="info" >}}
For most users, only the **Required Scopes** are needed. Skip Organization (Read) unless you plan to use `policy.team_ids` in your Repository CR configuration.
{{< /callout >}}

Store the generated token in a safe place, or you will have to recreate it.

## Webhook Configuration (Manual)

{{< callout type="info" >}}
The `tkn pac create repo` and `tkn pac webhook` commands do not currently support Forgejo. You must configure the webhook manually.
{{< /callout >}}

1. From your Forgejo repository, go to **Settings** -> **Webhooks** and click **Add Webhook** -> **Forgejo**.

2. Set the **HTTP method** to **POST** and **POST content type** to **application/json**.

3. Set the **Target URL** to the Pipelines-as-Code controller public URL. On OpenShift, you can get the public URL like this:

   ```shell
   echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
   ```

   _If you are not using OpenShift you will need to get the public route from your ingress controller._

4. Set a **Secret** or generate a random one with:

   ```shell
   head -c 30 /dev/random | base64
   ```

5. Select the following **Trigger On** events under **Custom events...** (these map to the events Pipelines-as-Code processes):

   **Repository events:**
   - Push

   **Pull request events:**
   - Opened
   - Reopened
   - Synchronized
   - Label updated
   - Closed

   **Issue events:**
   - Comments (only comments on open pull requests are processed)

6. Click **Add Webhook**.

### Create the Secret

Create a Kubernetes secret containing your personal token and the webhook secret in your target namespace:

```shell
kubectl -n target-namespace create secret generic forgejo-webhook-config \
  --from-literal provider.token="TOKEN_AS_GENERATED_PREVIOUSLY" \
  --from-literal webhook.secret="SECRET_AS_SET_IN_WEBHOOK_CONFIGURATION"
```

If you configured an empty webhook secret, use an empty string:

```shell
kubectl -n target-namespace create secret generic forgejo-webhook-config \
  --from-literal provider.token="TOKEN_AS_GENERATED_PREVIOUSLY" \
  --from-literal webhook.secret=""
```

### Create the Repository CR

Create a [`Repository` CR]({{< relref "/docs/guides/repository-crd" >}}) with the secret field referencing it:

```yaml
---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: my-repo
  namespace: target-namespace
spec:
  url: "https://forgejo.example.com/owner/repo"
  git_provider:
    type: "forgejo"
    # Set this to your Forgejo instance URL
    url: "https://forgejo.example.com"
    secret:
      name: "forgejo-webhook-config"
      # Set this if you have a different key in your secret
      # key: "provider.token"
    webhook_secret:
      name: "forgejo-webhook-config"
      # Set this if you have a different key in your secret
      # key: "webhook.secret"
```

## Notes

- **Provider Type**: Use `type: "forgejo"` in your Repository CR. The legacy `type: "gitea"` is kept as an alias for backwards compatibility.

- **Forgejo Instance URL**: Specify `git_provider.url` pointing to your Forgejo instance URL.

- **Webhook Secret**: Pipelines-as-Code validates webhook signatures for Forgejo/Gitea using HMAC-SHA256. A webhook secret must be configured both in the Forgejo webhook settings and in the Kubernetes secret referenced by the Repository CR. Requests without a valid signature will be rejected.

- The `git_provider.secret` key cannot reference a secret in another namespace. Pipelines-as-Code always assumes it is in the same namespace where the Repository CR has been created.

## Update Token

When you have regenerated a new token, you must update it in the cluster. You can find the secret name in the Repository CR:

```yaml
spec:
  git_provider:
    secret:
      name: "forgejo-webhook-config"
```

Replace `$NEW_TOKEN` and `$target_namespace` with your values:

```shell
kubectl -n target_namespace patch secret forgejo-webhook-config -p "{\"data\": {\"provider.token\": \"$(echo -n $NEW_TOKEN|base64 -w0)\"}}"
```
