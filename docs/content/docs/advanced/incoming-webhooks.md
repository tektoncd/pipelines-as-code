---
title: Incoming Webhook
weight: 4
---

This page explains how to configure and use incoming webhooks to trigger PipelineRuns via HTTP requests. Use incoming webhooks when you need to start a pipeline from an external system -- such as a CI orchestrator, a cron job, or a deployment script -- without pushing a new commit to the repository.

## Overview

Pipelines-as-Code supports incoming webhook URLs. Instead of requiring a new code change to trigger a pipeline, you can send an HTTP POST request with a shared secret to start a PipelineRun directly. This is useful for triggering pipelines from tools like `curl`, web services, or automation platforms.

## Incoming Webhook URL

To use incoming webhooks, configure the
`incoming` field in your Repository CR. This field references a `Secret` that
serves as the shared secret, and specifies the branches targeted by the incoming
webhook. Once configured, Pipelines-as-Code matches PipelineRuns located in
your `.tekton/` directory whose `on-event` annotation targets a push or incoming event.

{{< callout type="info" >}}
If you are not using the GitHub App provider (that is, you use a webhook-based provider), include a `git_provider` spec to specify a token.

Because Pipelines-as-Code cannot automatically detect the provider type
from the URL, you must also set the `git_provider.type` field. Supported
values are:

- github
- gitlab
- forgejo
- gitea (alias for forgejo)
- bitbucket-cloud
- bitbucket-datacenter

For `github-apps`, this is not required.
{{< /callout >}}

### Required Parameters

Whether you use the recommended POST request body or the deprecated query parameter method,
the `/incoming` endpoint accepts the following parameters:

| Parameter   | Type   | Description                                                                          | Required                                          |
|-------------|--------|--------------------------------------------------------------------------------------|---------------------------------------------------|
|`repository` |`string`| Name of Repository CR                                                                | `true`                                            |
|`namespace`  |`string`| Namespace with the Repository CR                                                     | When Repository name is not unique in the cluster |
|`branch`     |`string`| Branch configured for incoming webhook                                               | `true`                                            |
|`pipelinerun`|`string`| Name (or generateName) of PipelineRun, used to match PipelineRun definition          | `true`                                            |
|`secret`     |`string`| Secret key referenced by the Repository CR in desired incoming webhook configuration | `true`                                            |
|`params`     |`json`  | Parameters to override in PipelineRun context                                        | `false`                                           |

### GitHub App

The following example shows how to trigger a PipelineRun
through an incoming webhook URL when using the GitHub App provider.

The Repository CR targets the `main` branch and references a shared password stored in a
Secret called `repo-incoming-secret`:

```yaml
---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  incoming:
    - targets:
        - main
      secret:
        name: repo-incoming-secret
      type: webhook-url
```

{{< callout type="info" >}}
If no secret key is specified in the Repository CR, the default key `secret` is used to retrieve the secret value from the `repo-incoming-secret` Secret resource.
{{< /callout >}}

### Glob Pattern Matching in Targets

The `targets` field supports both exact string matching and glob patterns, allowing you to match multiple branches with a single rule.

**Glob patterns:** Use shell-style patterns:

- `*` - matches any characters (e.g., `feature/*` matches `feature/login`, `feature/api`)
- `?` - matches exactly one character (e.g., `v?` matches `v1`, `v2`)
- `[abc]` - matches one character from set (e.g., `[A-Z]*` matches any uppercase letter)
- `[0-9]` - matches digits (e.g., `v[0-9]*.[0-9]*` matches `v1.2`, `v10.5`)
- `{a,b,c}` - matches alternatives (e.g., `{dev,staging}/*` matches `dev/test` or `staging/test`)

**First-match-wins:** If multiple incoming webhooks match the same branch, the first matching webhook in the YAML order is used. Place more specific webhooks before general catch-all webhooks.

#### Examples

**Match feature branches with glob:**

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  incoming:
    - targets:
        - "feature/*"  # Matches any branch starting with "feature/"
      secret:
        name: feature-webhook-secret
      type: webhook-url
```

**Multiple webhooks with first-match-wins:**

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  incoming:
    # Production - checked first (most specific)
    - targets:
        - main
        - "v[0-9]*.[0-9]*.[0-9]*"  # Semver tags like v1.2.3
      secret:
        name: prod-webhook-secret
      params:
        - prod_env
      type: webhook-url

    # Feature branches - checked second
    - targets:
        - "feature/*"
        - "bugfix/*"
      secret:
        name: feature-webhook-secret
      params:
        - dev_env
      type: webhook-url

    # Catch-all - checked last
    - targets:
        - "*"  # Matches any branch not caught above
      secret:
        name: default-webhook-secret
      type: webhook-url
```

**Mix exact matches and glob patterns:**

```yaml
incoming:
  - targets:
      - main                            # Exact match
      - staging                         # Exact match
      - "release/v[0-9]*.[0-9]*.[0-9]*" # Semver releases
      - "hotfix/[A-Z]*-[0-9]*"          # JIRA tickets (e.g., JIRA-123, PROJ-456)
      - "{dev,test,qa}/*"               # Alternation pattern
    secret:
      name: repo-incoming-secret
    type: webhook-url
```

**Glob Pattern Syntax:**

- `*` - matches any characters (zero or more)
- `?` - matches exactly one character
- `[abc]` - matches one character: a, b, or c
- `[a-z]` - matches one character in range a to z
- `[0-9]` - matches one digit
- `{a,b,c}` - matches any of the alternatives (alternation)

**Best Practices:**

- Place production/sensitive webhooks first in the list
- Use exact matches for known branches when possible (faster than glob patterns)
- Use character classes `[0-9]`, `[A-Z]` for more precise matching
- Glob patterns match the entire branch name (no partial matches unless you use `*` prefix/suffix)
- Test your patterns: branch `feature-login` matches `feature-*` but not `*feature*`
- [Test your glob patterns online](https://www.digitalocean.com/community/tools/glob) before deploying to ensure they match only intended branches

### Using incoming webhooks

To receive incoming webhook triggers, annotate your PipelineRun to target the incoming event and the desired branch:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: target-pipelinerun
  annotations:
    pipelinesascode.tekton.dev/on-event: "[incoming]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
```

Next, create a secret called `repo-incoming-secret` that serves as the shared password. This ensures
that only authorized callers can trigger the PipelineRun:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: repo-incoming-secret
  namespace: ns
type: Opaque
stringData:
  secret: very-secure-shared-secret
```

After setting this up, trigger the PipelineRun by sending a POST
request to the controller URL appended with `/incoming`. Include the
shared secret (`very-secure-shared-secret`), the repository name (`repo`), the
target branch (`main`), and the PipelineRun name. You can pass these values either in the POST JSON body (recommended) or as URL query parameters (deprecated).

You can use the `generateName` field as the PipelineRun name, but make sure to include the trailing hyphen (`-`).

#### Legacy (URL query) method (deprecated)

```shell
curl -X POST 'https://control.pac.url/incoming?secret=very-secure-shared-secret&repository=repo&branch=main&pipelinerun=target-pipelinerun'
```

{{< callout type="warning" >}}
**Deprecated**: Passing secrets in the URL is insecure because query parameters appear in server logs and browser history. This method will be removed in a future release. Use the POST body method below instead.
{{< /callout >}}

#### Recommended (POST JSON body) method

```shell
curl -H "Content-Type: application/json" -X POST "https://control.pac.url/incoming" -d '{"repository":"repo","branch":"main","pipelinerun":"target-pipelinerun","secret":"very-secure-shared-secret"}'
```

In both cases, the `"/incoming"` path and the `"POST"` method remain the same.

When you trigger a PipelineRun this way, Pipelines-as-Code treats it as a push event and reports the
status accordingly. To receive notifications, you can add a `finally` task
to your Pipeline or inspect the Repository CR
using the `tkn pac` CLI. See the [statuses]({{< relref "/docs/guides/statuses" >}}) documentation
for details.

### Passing dynamic parameter values to incoming webhooks

You can override any Pipelines-as-Code parameter, including
[built-in variables]({{< relref "/docs/guides/creating-pipelines#dynamic-variables" >}}), through the incoming webhook request.

To do this, list the parameter names in the `params` section of the
Repository CR and pass their values in the JSON body of the incoming webhook
request. Set the `Content-Type` header to `application/json`.

The following Repository CR allows passing the `pull_request_number` dynamic variable:

```yaml
---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  incoming:
    - targets:
        - main
      params:
        - pull_request_number
      secret:
        name: repo-incoming-secret
      type: webhook-url
```

Here is a `curl` command that passes the `pull_request_number` value:

```shell
curl -H "Content-Type: application/json" -X POST "https://control.pac.url/incoming" -d '{"repository":"repo","branch":"main","pipelinerun":"target-pipelinerun","secret":"very-secure-shared-secret","params": {"pull_request_number": "12345"}}'
```

Pipelines-as-Code sets the `pull_request_number` parameter to `12345`, so any use of
`{{pull_request_number}}` in your PipelineRun resolves to that value.

### Using incoming webhooks with GitHub Enterprise

Before the first GitHub Enterprise incoming webhook, the controller must have
processed a signed webhook from that GitHub instance. This records the trusted
GitHub host in the controller's GitHub App Secret. If discovery has not happened
yet, the incoming request fails with an initialization error.

The incoming request does not select or override the GitHub API endpoint.

```shell
curl -H "Content-Type: application/json" \
  -X POST "https://control.pac.url/incoming" \
  -d '{"repository":"repo","branch":"main","pipelinerun":"target-pipelinerun","secret":"very-secure-shared-secret"}'
```

### Using incoming webhooks with webhook-based providers

Webhook-based providers (GitHub Webhook, GitLab, Bitbucket, and others) also support
incoming webhooks, authenticating with the token from the `git_provider` section.

The following example shows a Repository CR that targets the `main` branch using a GitHub webhook provider:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  git_provider:
    type: github
    secret:
      name: "owner-token"
  incoming:
    - targets:
        - main
      secret:
        name: repo-incoming-secret
      type: webhook-url
```

As described above, you must also create the `repo-incoming-secret` Secret containing the shared password.
