---
title: "Repository Spec"
weight: 2
---

This page documents every field available under the Repository CR `spec`. Use this reference when configuring a Repository CR for your Git repository. The `spec` defines the desired state of a Repository, including its URL, Git provider configuration, and operational settings.

## Fields

{{< param name="url" type="string" required="true" >}}
Specifies the repository URL. Must be a valid HTTP/HTTPS Git repository URL. Pipelines-as-Code uses this URL to clone the repository and fetch pipeline definitions from the `.tekton/` directory.

```yaml
spec:
  url: "https://github.com/owner/repository"
```

{{< /param >}}

{{< param name="concurrency_limit" type="integer" id="param-concurrency-limit" >}}
Sets the maximum number of concurrent PipelineRuns for this repository. This prevents resource exhaustion when many events trigger pipelines simultaneously. Minimum value: 1.

```yaml
spec:
  concurrency_limit: 5
```

{{< /param >}}

{{< param name="git_provider" type="GitProvider" id="param-git-provider" >}}
Configures how Pipelines-as-Code connects to your Git provider. Contains authentication credentials, API endpoints, and provider type information.

{{< param-group label="Show GitProvider Fields" >}}

{{< param name="git_provider.type" type="string" id="param-git-provider-type" >}}
Identifies the Git provider type. Pipelines-as-Code uses this to select the correct API and authentication flow. Supported values:

- `github` - GitHub.com or GitHub Enterprise
- `gitlab` - GitLab.com or self-hosted GitLab
- `bitbucket-datacenter` - Bitbucket Data Center (self-hosted)
- `bitbucket-cloud` - Bitbucket Cloud (bitbucket.org)
- `forgejo` - Forgejo instances
- `gitea` - Gitea instances (alias for forgejo, kept for backwards compatibility)

```yaml
git_provider:
  type: github
```

{{< /param >}}

{{< param name="git_provider.url" type="string" id="param-git-provider-url" >}}
Specifies the Git provider API endpoint. Pipelines-as-Code sends API requests to this base URL (for example, `https://api.github.com` for GitHub or a custom GitLab instance URL).

```yaml
git_provider:
  url: "https://gitlab.example.com"
```

{{< /param >}}

{{< param name="git_provider.user" type="string" id="param-git-provider-user" >}}
Sets the username for basic auth or token-based authentication. Pipelines-as-Code does not use this field for GitHub App authentication.
For Bitbucket Cloud, set this to the Atlassian account email that owns the
scoped API token.

```yaml
git_provider:
  user: "pac-bot"
```

{{< /param >}}

{{< param name="git_provider.secret" type="Secret" id="param-git-provider-secret" >}}
References a Kubernetes Secret containing the credentials (token, password, or private key) that Pipelines-as-Code uses to authenticate with the Git provider API.

{{< param-group label="Show Secret Fields" >}}

{{< param name="secret.name" type="string" required="true" id="param-secret-name" >}}
Name of the Kubernetes secret.
{{< /param >}}

{{< param name="secret.key" type="string" id="param-secret-key" >}}
Key within the secret containing the value.
{{< /param >}}

{{< /param-group >}}

```yaml
git_provider:
  secret:
    name: github-token
    key: token
```

{{< /param >}}

{{< param name="git_provider.webhook_secret" type="Secret" id="param-git-provider-webhook-secret" >}}
References a Kubernetes Secret containing the shared secret that Pipelines-as-Code uses to validate that incoming webhooks are legitimate and originate from the Git provider.

```yaml
git_provider:
  webhook_secret:
    name: webhook-secret
    key: secret
```

{{< /param >}}

{{< /param-group >}}

```yaml
spec:
  git_provider:
    type: github
    url: "https://github.com"
    user: "pac-bot"
    secret:
      name: github-token
      key: token
```

{{< /param >}}

{{< param name="incoming" type="[]Incoming" >}}
Configures incoming webhooks. Each entry specifies how Pipelines-as-Code handles external webhook requests that do not come from the primary Git provider.

{{< param-group label="Show Incoming Fields" >}}

{{< param name="incoming[].type" type="string" required="true" id="param-incoming-type" >}}
Specifies the incoming webhook type. Currently only `webhook-url` is supported, which allows external systems to trigger PipelineRuns via generic HTTP requests.
{{< /param >}}

{{< param name="incoming[].secret" type="Secret" required="true" id="param-incoming-secret" >}}
References the Kubernetes Secret that Pipelines-as-Code uses to authenticate incoming webhook requests. Only requests with the matching secret value are accepted.

{{< param-group label="Show Secret Fields" >}}

{{< param name="secret.name" type="string" required="true" id="param-incoming-secret-name" >}}
Name of the Kubernetes secret.
{{< /param >}}

{{< param name="secret.key" type="string" id="param-incoming-secret-key" >}}
Key within the secret containing the value.
{{< /param >}}

{{< /param-group >}}
{{< /param >}}

{{< param name="incoming[].params" type="[]string" id="param-incoming-params" >}}
Lists parameter names to extract from the webhook payload. Pipelines-as-Code makes these parameters available to PipelineRuns triggered by this webhook.
{{< /param >}}

{{< param name="incoming[].targets" type="[]string" id="param-incoming-targets" >}}
Lists the target branches for this webhook. Pipelines-as-Code triggers PipelineRuns only when the incoming request specifies one of these branches.
{{< /param >}}

{{< /param-group >}}

```yaml
spec:
  incoming:
    - type: webhook-url
      secret:
        name: webhook-secret
        key: token
      params:
        - branch
        - revision
      targets:
        - main
        - develop
```

{{< /param >}}

{{< param name="params" type="[]Params" >}}
Defines repository-level parameters that you can reference in PipelineRuns. Use these for default values or event-specific configuration.

{{< param-group label="Show Params Fields" >}}

{{< param name="params[].name" type="string" required="true" id="param-params-name" >}}
Sets the parameter name. Use this name to reference the parameter in PipelineRun definitions through the `{{ name }}` syntax.
{{< /param >}}

{{< param name="params[].value" type="string" id="param-params-value" >}}
Sets the parameter value as a literal string. Pipelines-as-Code provides this value to the PipelineRun. This field is mutually exclusive with `secret_ref`.
{{< /param >}}

{{< param name="params[].secret_ref" type="Secret" id="param-params-secret-ref" >}}
References a Kubernetes Secret containing the parameter value. Use this when the parameter contains sensitive information that you should not store directly in the Repository CR. This field is mutually exclusive with `value`.

{{< param-group label="Show Secret Fields" >}}

{{< param name="secret.name" type="string" required="true" id="param-params-secret-name" >}}
Name of the Kubernetes secret.
{{< /param >}}

{{< param name="secret.key" type="string" id="param-params-secret-key" >}}
Key within the secret containing the value.
{{< /param >}}

{{< /param-group >}}
{{< /param >}}

{{< param name="params[].filter" type="string" id="param-params-filter" >}}
Defines a CEL expression that controls when Pipelines-as-Code applies this parameter. Use this to conditionally apply parameters based on event type, branch name, or other attributes.
{{< /param >}}

{{< /param-group >}}

```yaml
spec:
  params:
    - name: deployment_env
      value: production
      filter: "event == 'push' && target_branch == 'main'"
    - name: api_key
      secret_ref:
        name: api-credentials
        key: key
```

{{< /param >}}

{{< param name="settings" type="Settings" >}}
Configures repository-level settings, including authorization policies, provider-specific behavior, and provenance settings. See [Settings Reference]({{< relref "settings" >}}) for detailed documentation.

```yaml
spec:
  settings:
    pipelinerun_provenance: "source"
    policy:
      ok_to_test:
        - "trusted-user"
```

{{< /param >}}

## Complete example

```yaml
spec:
  url: "https://github.com/organization/repository"
  concurrency_limit: 3
  git_provider:
    type: github
    url: "https://github.com"
    user: "pac-bot"
    secret:
      name: github-token
      key: token
    webhook_secret:
      name: webhook-secret
      key: secret
  incoming:
    - type: webhook-url
      secret:
        name: incoming-webhook-secret
        key: token
      params:
        - version
        - environment
      targets:
        - main
  params:
    - name: cluster_name
      value: "production-cluster"
    - name: registry_token
      secret_ref:
        name: registry-credentials
        key: token
      filter: "event == 'push'"
  settings:
    pipelinerun_provenance: "source"
    policy:
      ok_to_test:
        - "maintainer-user"
        - "trusted-contributor"
      pull_request:
        - "external-contributor"
    github:
      comment_strategy: "update"
```
