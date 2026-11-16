---
title: CEL Expressions for Variables
weight: 1
---

This page covers how to use CEL expressions to access webhook payload data and construct dynamic values in your PipelineRuns. CEL (Common Expression Language) is a lightweight expression language designed for evaluating structured data. Pipelines-as-Code uses it to let you build conditions and extract values from webhook payloads at runtime.

## Using the body and headers in a Pipelines-as-Code parameter

Pipelines-as-Code gives you access to the full body and headers of the webhook request as CEL expressions. This lets you go beyond the standard dynamic variables and combine multiple conditions and values from the raw payload.

For example, to get the title of a pull request in your PipelineRun, access it like this:

```go
{{ body.pull_request.title }}
```

You can also embed these variables inside a script to evaluate JSON data at runtime.

The following task uses Python to check the labels on a pull request. It exits with `0` if the pull request has the label `bug`, or `1` if it does not:

```yaml
taskSpec:
  steps:
    - name: check-label
      image: registry.access.redhat.com/ubi10/ubi
      script: |
        #!/usr/bin/env python3
        import json
        labels=json.loads("""{{ body.pull_request.labels }}""")
        for label in labels:
            if label['name'] == 'bug':
              print('This is a PR targeting a BUG')
              exit(0)
        print('This is not a PR targeting a BUG :(')
        exit(1)
```

Because Pipelines-as-Code evaluates expressions as CEL, you can use conditionals directly:

```yaml
- name: bash
  image: registry.access.redhat.com/ubi10/ubi
  script: |
    if {{ body.pull_request.state == "open" }}; then
      echo "PR is Open"
    fi
```

If the pull request is open, the condition returns `true`, which the shell script interprets as a valid boolean.

You can access headers from the webhook payload using the `headers` keyword. Headers are case-sensitive. For example, the following retrieves the GitHub event type:

```yaml
{{ headers['X-Github-Event'] }}
```

You can apply the same conditional logic and access patterns described above for the `body` keyword to headers as well.

## Using the cel: prefix for advanced CEL expressions

When you need more than simple property access, use the `cel:` prefix to write arbitrary CEL expressions with access to all available data sources. This is useful for conditional logic, string manipulation, and checking changed files.

The `cel:` prefix provides access to:

- `body` - The full webhook payload
- `headers` - HTTP request headers
- `files` - Changed files information (`files.all`, `files.added`, `files.deleted`, `files.modified`, `files.renamed`)
- `pac` - Standard Pipelines-as-Code parameters (`pac.revision`, `pac.target_branch`, `pac.source_branch`, etc.)

```mermaid
flowchart LR
%% title: How a cel: expression is turned into a parameter value
  Body@{ img: "/icons/webhook.svg", label: "body", w: 48, h: 48, constraint: "on" }
  Head@{ img: "/icons/document.svg", label: "headers", w: 48, h: 48, constraint: "on" }
  Files@{ img: "/icons/folder.svg", label: "files", w: 48, h: 48, constraint: "on" }
  Pac@{ img: "/icons/config.svg", label: "pac", w: 48, h: 48, constraint: "on" }
  Cel@{ img: "/icons/code.svg", label: "cel: expression", w: 48, h: 48, constraint: "on" }
  Val@{ img: "/icons/success.svg", label: "Value", w: 48, h: 48, constraint: "on" }
  Empty@{ img: "/icons/stop.svg", label: "Empty string", w: 48, h: 48, constraint: "on" }
  Body --> Cel
  Head --> Cel
  Files --> Cel
  Pac --> Cel
  Cel ok@-->|evaluates| Val
  Cel bad@-->|error| Empty

%% play: Branch to environment
%% skip Body Head Files Empty
%% Pac -> Cel : main | pac.target_branch == "main" ? "production" : "staging"
%% Cel -> Val @ok : production
%% play: Go files changed
%% skip Body Head Pac Empty
%% Files -> Cel : files.all | files.all.exists(f, f.endsWith(".go"))
%% Cel -> Val @ok : "true"
%% play: Broken expression
%% skip Head Files Pac Val
%% Body -> Cel : payload | An expression that does not compile or fails to evaluate
%% wait 800
%% Cel -> Empty @bad : "" | The parameter gets an empty string and the PipelineRun still runs
```

The following examples demonstrate common patterns.

## Examples

**Conditional values based on event action:**

```yaml
params:
  - name: pr-status
    value: "{{ cel: body.action == \"opened\" ? \"new-pr\" : \"updated-pr\" }}"
```

**Environment selection based on target branch:**

```yaml
params:
  - name: environment
    value: "{{ cel: pac.target_branch == \"main\" ? \"production\" : \"staging\" }}"
```

**Safe field access with has() function:**

Use the `has()` function to safely check if a field exists before accessing it:

```yaml
params:
  - name: commit-type
    value: "{{ cel: has(body.head_commit) && body.head_commit.message.startsWith(\"Merge\") ? \"merge\" : \"regular\" }}"
```

**Check if Go files were modified:**

```yaml
params:
  - name: run-go-tests
    value: "{{ cel: files.all.exists(f, f.endsWith(\".go\")) ? \"true\" : \"false\" }}"
```

**String concatenation:**

```yaml
params:
  - name: greeting
    value: "{{ cel: \"Build for \" + pac.repo_name + \" on \" + pac.target_branch }}"
```

**Count changed files:**

```yaml
params:
  - name: file-count
    value: "{{ cel: files.all.size() }}"
```

{{< callout type="info" >}}
If a `cel:` expression has a syntax error or fails to evaluate, Pipelines-as-Code returns an
empty string. This allows PipelineRuns to continue even if an optional dynamic
value cannot be computed.
{{< /callout >}}
