---
title: "ConfigMap Reference"
weight: 4
---

This page documents every field in the Pipelines-as-Code global ConfigMap. Use this reference when you need to change cluster-wide defaults for all repositories. Individual Repository CRs can override most of these settings.

## Location

Pipelines-as-Code installs the ConfigMap in the `pipelines-as-code` namespace by default:

```bash
kubectl get configmap pipelines-as-code -n pipelines-as-code
```

## Configuration Fields

### Application Settings

{{< param name="application-name" type="string" default="Pipelines as Code CI" id="param-application-name" >}}
Sets the application name that Pipelines-as-Code displays in status updates and comments. If you use the GitHub App, also customize this label in the GitHub App settings.

```yaml
application-name: "Pipelines as Code CI"
```

{{< /param >}}

### Secret Management

{{< param name="secret-auto-create" type="boolean" default="true" id="param-secret-auto-create" >}}
Controls whether Pipelines-as-Code automatically creates a secret containing the Git provider token for use by the git-clone task.

```yaml
secret-auto-create: "true"
```

{{< /param >}}

{{< param name="secret-github-app-token-scoped" type="boolean" default="true" id="param-secret-github-app-token-scoped" >}}
Controls whether Pipelines-as-Code scopes generated tokens to only the repository that triggered the event. This setting is important when the GitHub App is installed on an organization with a mix of public and private repositories where some users should not access all repositories. Set to `false` if you trust every user in your organization to access all repositories, or if you are not installing your GitHub App at the organization level.

```yaml
secret-github-app-token-scoped: "true"
```

{{< /param >}}

{{< param name="secret-github-app-scope-extra-repos" type="string" id="param-secret-github-app-scope-extra-repos" >}}
Adds extra repositories to the token scope without disabling scoping entirely. List additional `owner/repo` pairs (on the same installation ID), separated by commas. Use this when your pipeline needs to access a shared library or dependency repository.

```yaml
secret-github-app-scope-extra-repos: "owner/private-repo1, org/repo2"
```

{{< /param >}}

### Hub Configuration

{{< param name="hub-url" type="string" default="<https://artifacthub.io/api/v1>" id="param-hub-url" >}}
Specifies the default hub API URL that Pipelines-as-Code uses to fetch remote tasks.

```yaml
hub-url: "https://artifacthub.io/api/v1"
```

{{< /param >}}

{{< param name="hub-catalog-type" type="string" default="artifacthub" id="param-hub-catalog-type" >}}
Sets the default hub catalog type. Supported values: `artifacthub`, `tektonhub`.

```yaml
hub-catalog-type: "artifacthub"
```

{{< /param >}}

{{< param name="catalog-{N}-*" type="object" id="param-catalog-n" >}}
Configures additional hub catalogs. You can define multiple catalogs by incrementing the number (catalog-1-*, catalog-2-*, etc.).

{{< param-group label="Show Catalog Configuration Fields" >}}

{{< param name="catalog-{N}-id" type="string" id="param-catalog-n-id" >}}
Unique identifier for this catalog.
{{< /param >}}

{{< param name="catalog-{N}-name" type="string" id="param-catalog-n-name" >}}
Name of the catalog.
{{< /param >}}

{{< param name="catalog-{N}-url" type="string" id="param-catalog-n-url" >}}
URL of the catalog API endpoint.
{{< /param >}}

{{< param name="catalog-{N}-type" type="string" id="param-catalog-n-type" >}}
Type of catalog (`tektonhub` or `artifacthub`).
{{< /param >}}

{{< /param-group >}}

```yaml
catalog-1-id: "custom"
catalog-1-name: "tekton"
catalog-1-url: "https://api.custom.hub/v1"
catalog-1-type: "tektonhub"
```

{{< /param >}}

{{< param name="remote-tasks" type="boolean" default="true" id="param-remote-tasks" >}}
Controls whether Pipelines-as-Code fetches remote tasks from configured hubs.

```yaml
remote-tasks: "true"
```

{{< /param >}}

### Dashboard Integration

{{< param name="tekton-dashboard-url" type="string" id="param-tekton-dashboard-url" >}}
Sets the Tekton dashboard URL. Pipelines-as-Code uses this base URL to generate links to PipelineRun details in status reports.

```yaml
tekton-dashboard-url: "https://tekton.example.com"
```

{{< /param >}}

{{< param name="custom-console-name" type="string" id="param-custom-console-name" >}}
Sets the display name for a custom console to use instead of the Tekton dashboard.

```yaml
custom-console-name: "Console Name"
```

{{< /param >}}

{{< param name="custom-console-url" type="string" id="param-custom-console-url" >}}
Sets the base URL of the custom console.

```yaml
custom-console-url: "https://url"
```

{{< /param >}}

{{< param name="custom-console-url-pr-details" type="string" id="param-custom-console-url-pr-details" >}}
Defines the template URL for PipelineRun details. Supports variables: `{{ namespace }}`, `{{ pr }}`.

```yaml
custom-console-url-pr-details: "https://url/ns/{{ namespace }}/{{ pr }}"
```

{{< /param >}}

{{< param name="custom-console-url-pr-tasklog" type="string" id="param-custom-console-url-pr-tasklog" >}}
Defines the template URL for task logs. Supports variables: `{{ namespace }}`, `{{ pr }}`, `{{ task }}`.

```yaml
custom-console-url-pr-tasklog: "https://url/ns/{{ namespace }}/{{ pr }}/logs/{{ task }}"
```

{{< /param >}}

{{< param name="custom-console-url-namespace" type="string" id="param-custom-console-url-namespace" >}}
Defines the template URL for namespace-level views in your custom console.
Supports the `{{ namespace }}` variable.

```yaml
custom-console-url-namespace: "https://url/ns/{{ namespace }}"
```

{{< /param >}}

### Error Detection and Logging

{{< param name="error-log-snippet" type="boolean" default="true" id="param-error-log-snippet" >}}
Controls whether Pipelines-as-Code shows a log snippet from the failed task when a Pipeline encounters an error. Disable this setting if your pipeline output may contain sensitive values.

```yaml
error-log-snippet: "true"
```

{{< /param >}}

{{< param name="error-log-snippet-number-of-lines" type="integer" default="3" id="param-error-log-snippet-number-of-lines" >}}
Sets the number of lines to display in error log snippets when `error-log-snippet` is `true`. Keep this value conservative because the GitHub Check interface has a 65,535 character limit.

```yaml
error-log-snippet-number-of-lines: "3"
```

{{< /param >}}

{{< param name="error-detection-from-container-logs" type="boolean" default="true" id="param-error-detection-from-container-logs" >}}
Controls whether Pipelines-as-Code inspects container logs to detect error messages and exposes them as annotations on pull requests. Only GitHub Apps are supported.

```yaml
error-detection-from-container-logs: "true"
```

{{< /param >}}

{{< param name="error-detection-max-number-of-lines" type="integer" default="50" id="param-error-detection-max-number-of-lines" >}}
Sets how many lines Pipelines-as-Code reads from the container when inspecting logs for error detection. Increasing this value may increase watcher memory usage. Use `-1` for unlimited lines.

```yaml
error-detection-max-number-of-lines: "50"
```

{{< /param >}}

{{< param name="error-detection-simple-regexp" type="string" id="param-error-detection-simple-regexp" >}}
Sets the default regular expression used for simple error detection. Must be a valid regular expression.

```yaml
error-detection-simple-regexp: |
  ^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+)?([ ]*)?(?P<error>.*)
```

{{< /param >}}

### Concurrency Control

{{< param name="enable-cancel-in-progress-on-pull-requests" type="boolean" default="false" id="param-enable-cancel-in-progress-on-pull-requests" >}}
Controls whether Pipelines-as-Code automatically cancels in-progress PipelineRuns for a pull request when that pull request receives a new push. This prevents redundant runs from consuming cluster resources.

```yaml
enable-cancel-in-progress-on-pull-requests: "false"
```

{{< /param >}}

{{< param name="enable-cancel-in-progress-on-push" type="boolean" default="false" id="param-enable-cancel-in-progress-on-push" >}}
Controls whether Pipelines-as-Code automatically cancels in-progress PipelineRuns triggered by a push event when a new push occurs on the same branch. This prevents overlapping runs for the same branch.

```yaml
enable-cancel-in-progress-on-push: "false"
```

{{< /param >}}

### Bitbucket Cloud Settings

{{< param name="bitbucket-cloud-check-source-ip" type="boolean" default="true" id="param-bitbucket-cloud-check-source-ip" >}}
Controls whether Pipelines-as-Code validates incoming webhook requests against Bitbucket Cloud's published IP ranges at <https://ip-ranges.atlassian.com/>. Because public Bitbucket does not support webhook secrets, IP verification is the primary security mechanism. This check applies only to public Bitbucket (when `provider.url` is not set in the Repository CR spec).

Disabling this setting creates a security risk. A malicious user could submit a pull request with a modified PipelineRun that exfiltrates secrets, then send a forged webhook payload to trigger it.

```yaml
bitbucket-cloud-check-source-ip: "true"
```

{{< /param >}}

{{< param name="bitbucket-cloud-additional-source-ip" type="string" id="param-bitbucket-cloud-additional-source-ip" >}}
Adds extra IPs (for example, `127.0.0.1`) or networks (for example, `127.0.0.0/16`) to the allowed list, separated by commas.

```yaml
bitbucket-cloud-additional-source-ip: "192.168.1.0/24, 10.0.0.1"
```

{{< /param >}}

### Retention Policies

These settings control the global cleanup defaults. Individual PipelineRuns can
also set a per-run limit using the `pipelinesascode.tekton.dev/max-keep-runs`
annotation — see [PipelineRuns Cleanup]({{< relref "/docs/advanced/cleanup" >}}).

{{< param name="max-keep-run-upper-limit" type="integer" id="param-max-keep-run-upper-limit" >}}
Sets the maximum value that a user can specify in the `max-keep-run` annotation on a PipelineRun. If a user sets a value higher than this limit, Pipelines-as-Code uses the upper limit during cleanup instead.

```yaml
max-keep-run-upper-limit: "100"
```

{{< /param >}}

{{< param name="default-max-keep-runs" type="integer" id="param-default-max-keep-runs" >}}
Sets the default cleanup retention count. Pipelines-as-Code applies this value to all PipelineRuns that do not have the `max-keep-runs` annotation.

```yaml
default-max-keep-runs: "10"
```

{{< /param >}}

### Auto-Configuration

{{< param name="auto-configure-new-github-repo" type="boolean" default="false" id="param-auto-configure-new-github-repo" >}}
Controls whether Pipelines-as-Code automatically creates a namespace and Repository CR for newly created repositories. Supported only with the GitHub App.

```yaml
auto-configure-new-github-repo: "false"
```

{{< /param >}}

{{< param name="auto-configure-repo-namespace-template" type="string" id="param-auto-configure-repo-namespace-template" >}}
Defines the template for generating namespace names when auto-configuring GitHub repositories. Supported fields: `{{repo_owner}}`, `{{repo_name}}`.

```yaml
auto-configure-repo-namespace-template: "{{repo_owner}}-{{repo_name}}"
```

{{< /param >}}

{{< param name="auto-configure-repo-repository-template" type="string" id="param-auto-configure-repo-repository-template" >}}
Defines the template for generating Repository CR names when auto-configuring GitHub repositories. Supported fields: `{{repo_owner}}`, `{{repo_name}}`.

```yaml
auto-configure-repo-repository-template: "{{repo_owner}}-{{repo_name}}-repo-cr"
```

{{< /param >}}

### Security and Authorization

{{< param name="remember-ok-to-test" type="boolean" default="false" id="param-remember-ok-to-test" >}}
Controls whether Pipelines-as-Code remembers a previous `/ok-to-test` approval when new commits are pushed to a pull request. By default, users must issue `/ok-to-test` on each push. Set to `true` to persist the approval across push events.

```yaml
remember-ok-to-test: "false"
```

{{< /param >}}

{{< param name="require-ok-to-test-sha" type="boolean" default="false" id="param-require-ok-to-test-sha" >}}
Requires that a pull request's commit SHA be specified in an `/ok-to-test` comment. This prevents a race condition where a malicious user pushes a new commit after the `/ok-to-test` comment but before Pipelines-as-Code starts the CI run.

```yaml
require-ok-to-test-sha: "false"
```

{{< /param >}}

{{< param name="skip-push-event-for-pr-commits" type="boolean" default="true" id="param-skip-push-event-for-pr-commits" >}}
Prevents duplicate PipelineRuns when a commit appears in both a push event and a pull request. When a push event arrives from a commit that belongs to an open pull request, Pipelines-as-Code skips the push event.

```yaml
skip-push-event-for-pr-commits: "true"
```

{{< /param >}}

## Complete Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
  labels:
    app.kubernetes.io/part-of: pipelines-as-code
data:
  application-name: "My CI System"
  secret-auto-create: "true"
  secret-github-app-token-scoped: "true"
  secret-github-app-scope-extra-repos: "org/shared-repo"

  hub-url: "https://artifacthub.io/api/v1"
  hub-catalog-type: "artifacthub"
  remote-tasks: "true"

  tekton-dashboard-url: "https://tekton.example.com"

  error-log-snippet: "true"
  error-log-snippet-number-of-lines: "5"
  error-detection-from-container-logs: "true"
  error-detection-max-number-of-lines: "100"

  enable-cancel-in-progress-on-pull-requests: "true"
  enable-cancel-in-progress-on-push: "false"

  max-keep-run-upper-limit: "50"
  default-max-keep-runs: "10"

  remember-ok-to-test: "true"
  require-ok-to-test-sha: "false"
  skip-push-event-for-pr-commits: "true"
```

## Updating configuration

You can edit the ConfigMap directly:

```bash
kubectl edit configmap pipelines-as-code -n pipelines-as-code
```

Or apply changes from a YAML file:

```bash
kubectl apply -f pipelines-as-code-config.yaml
```

Most changes take effect immediately. Some settings may require you to restart the Pipelines-as-Code controller.

## See Also

- [Settings]({{< relref "/docs/operations/settings" >}}) - Overview of global and per-repository configuration layers
- [Configuration]({{< relref "/docs/operations/configuration" >}}) - How to view and apply ConfigMap changes
- [Repository CR Settings Reference]({{< relref "/docs/api/settings" >}}) - Per-repository overrides
- [PipelineRuns Cleanup]({{< relref "/docs/advanced/cleanup" >}}) - Per-run retention using annotations
- [Global Repository Settings]({{< relref "/docs/operations/global-repository-settings" >}}) - Configure default settings for all repositories
- [Logging Configuration]({{< relref "/docs/operations/logging" >}}) - Configure log levels and debugging
- [Metrics]({{< relref "/docs/operations/metrics" >}}) - Monitor Pipelines-as-Code with Prometheus
