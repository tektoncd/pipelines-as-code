---
title: Custom Parameters
weight: 3
---
This page explains how to define and use custom parameters in your PipelineRuns. Use custom parameters when you need to inject repository-level values, such as environment names or secrets, into your pipeline templates.

## Overview

Using the `{{ param }}` syntax, Pipelines-as-Code lets you expand a variable or
the payload body inside a template within your PipelineRun.

Pipelines-as-Code exposes several variables by default, depending on the event type. For the full list, refer to the [Authoring
PipelineRuns]({{< relref "/docs/guides/creating-pipelines#dynamic-variables" >}}) documentation.

Custom parameters let you define additional values that Pipelines-as-Code replaces inside the template at runtime.

{{< callout type="warning" >}}
Utilizing the Tekton PipelineRun parameters feature may generally be the
preferable approach, and custom params expansion should only be used in specific
scenarios where Tekton params cannot be used.
{{< /callout >}}

For example, here is a custom variable in the Repository CR `spec`:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
```

Pipelines-as-Code replaces the variable name `{{ company }}` with `My Beautiful Company`
anywhere inside your PipelineRun, including remotely fetched tasks.

You can also retrieve the value from a Kubernetes Secret.
For example, the following configuration retrieves the `company`
parameter from a secret named `my-secret` and the key `companyname`:

```yaml
spec:
  params:
    - name: company
      secret_ref:
        name: my-secret
        key: companyname
```

If no default value makes sense for a custom parameter, you can define it
without a value:

```yaml
spec:
  params:
    - name: start_time
```

If you define a custom parameter without a value, Pipelines-as-Code expands it only when a value is supplied via [a GitOps command]({{< relref "/docs/guides/gitops-commands/advanced#passing-parameters-to-gitops-commands-as-arguments" >}}).

{{< callout type="info" >}}

- If you define both a `value` and a `secret_ref`, Pipelines-as-Code uses the `value`.
- If you omit both `value` and `secret_ref`, and no [GitOps command]({{< relref "/docs/guides/gitops-commands/advanced#passing-parameters-to-gitops-commands-as-arguments" >}}) overrides the parameter,
  Pipelines-as-Code leaves the parameter unexpanded, and it appears as `{{ param }}` in
  the PipelineRun.
- If you omit `name` from the `params` entry, Pipelines-as-Code ignores the parameter.
- If you define multiple `params` with the same `name`, Pipelines-as-Code uses the last one.
{{< /callout >}}

### CEL filtering on custom parameters

You can restrict when Pipelines-as-Code expands a custom parameter by adding a `filter` field. The filter uses a CEL expression, and Pipelines-as-Code applies the parameter only when the expression evaluates to true:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
      filter: pac.event_type == "pull_request"
```

The `pac` prefix exposes all default template variables. Refer to the [Authoring PipelineRuns]({{< relref "/docs/guides/creating-pipelines" >}}) documentation
for the complete list.

The `body` prefix exposes the raw webhook payload.

For example, when you open a pull request on GitHub, the incoming payload contains JSON like this:

```json
{
  "action": "opened",
  "number": 79,
  // .... more data
}
```

You can then match against those payload fields in your filter:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
      filter: body.action == "opened" && pac.event_type == "pull_request"
```

The event payload contains additional fields that you can reference in your CEL filter. To see the full payload structure for your Git provider, refer to the provider's API documentation.

You can define multiple `params` with the same name but different filters. Pipelines-as-Code picks the first parameter whose filter matches. This lets you produce different values depending on the event type -- for example, using one value for push events and another for pull request events.

{{< callout type="info" >}}

- [GitHub Documentation for webhook events](https://docs.github.com/webhooks-and-events/webhooks/webhook-events-and-payloads?actionType=auto_merge_disabled#pull_request)
- [GitLab Documentation for webhook events](https://docs.gitlab.com/ee/user/project/integrations/webhook_events.html)
{{< /callout >}}

The following diagram shows how Pipelines-as-Code resolves each entry of `params` and which values can override the result:

```mermaid
flowchart LR
%% title: How Pipelines-as-Code resolves custom parameters
%% autoplay
  Entry@{ img: "/icons/config.svg", label: "params entry", w: 48, h: 48, constraint: "on" }
  Filter@{ img: "/icons/filter.svg", label: "CEL filter", w: 48, h: 48, constraint: "on" }
  Val@{ img: "/icons/document.svg", label: "value", w: 48, h: 48, constraint: "on" }
  Sec@{ img: "/icons/secret.svg", label: "secret_ref", w: 48, h: 48, constraint: "on" }
  Res@{ img: "/icons/check.svg", label: "Resolved params", w: 48, h: 48, constraint: "on" }
  Cmt@{ img: "/icons/comment.svg", label: "GitOps comment", w: 48, h: 48, constraint: "on" }
  Inc@{ img: "/icons/webhook.svg", label: "Incoming params", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "PipelineRun", w: 48, h: 48, constraint: "on" }
  Skip@{ img: "/icons/stop.svg", label: "Skipped", w: 48, h: 48, constraint: "on" }
  Entry nof@-->|no filter| Val
  Entry hasf@-->|filter| Filter
  Filter yes@-->|true| Val
  Filter no@-->|false or already matched| Skip
  Val set@-->|set| Res
  Val unset@-->|empty| Sec
  Sec --> Res
  Cmt -->|key=value| Res
  Inc -->|params| Res
  Res -->|"{{ param }}"| Run

%% play: Plain value
%% skip Filter Sec Cmt Inc Skip
%% Entry -> Val @nof : company | An entry with a value uses it, even when secret_ref is also set
%% Val -> Res @set
%% Res -> Run | Every {{ company }} in the PipelineRun is replaced
%% play: From a secret
%% skip Filter Cmt Inc Skip
%% Entry -> Val @nof
%% Val -> Sec @unset | Without a value, the key is read from a Secret in the Repository CR namespace
%% Sec -> Res
%% Res -> Run
%% play: Filtered
%% skip Sec Cmt Inc
%% Entry -> Filter @hasf : pull_request | The filter sees the pac variables and the webhook body
%% Filter -> Val @yes : true | The first entry with this name whose filter is true wins
%% Val -> Res @set
%% Entry -> Filter @hasf : push
%% Filter -> Skip @no | Later entries with the same name, or a false filter, are skipped
%% play: GitOps comment
%% skip Filter Sec Inc Skip
%% Entry -> Val @nof
%% Val -> Res @set
%% Cmt -> Res : company=acme | A key=value argument on a GitOps command overrides a param that is already defined
%% Res -> Run
%% play: Incoming webhook
%% skip Filter Sec Cmt Skip
%% Entry -> Val @nof
%% Val -> Res @set
%% Inc -> Res : params | Params sent to an incoming webhook are applied last and win
%% Res -> Run
```

### Using custom parameters in CEL matching expressions

Beyond template expansion (`{{ param }}`), you can also use custom parameters as CEL variables in the `on-cel-expression` annotation. This gives you control over which PipelineRuns Pipelines-as-Code triggers, based on repository-specific configuration.

For example, with this Repository CR configuration:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  params:
    - name: enable_ci
      value: "true"
    - name: environment
      value: "staging"
```

You can reference these parameters directly in your PipelineRun's CEL expression:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: my-pipeline
  annotations:
    pipelinesascode.tekton.dev/on-cel-expression: |
      event == "push" && enable_ci == "true" && environment == "staging"
spec:
  # ... pipeline spec
```

This approach is particularly useful for:

- **Conditional CI**: Enable or disable CI for specific repositories without changing PipelineRun files
- **Environment-specific matching**: Run different pipelines based on environment configuration
- **Feature flags**: Control which pipelines run using repository-level feature flags

Custom parameters sourced from secrets are also available in CEL expressions:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  params:
    - name: api_key
      secret_ref:
        name: my-secret
        key: key
```

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: my-pipeline-with-secret
  annotations:
    pipelinesascode.tekton.dev/on-cel-expression: |
      event == "push" && api_key != ""
spec:
  # ... pipeline spec
```

For more information on CEL expressions and event matching, see the [Advanced event matching using CEL]({{< relref "/docs/guides/event-matching/cel-expressions" >}}) documentation.
