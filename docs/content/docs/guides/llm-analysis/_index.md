---
title: AI/LLM-Powered Pipeline Analysis
weight: 8
---

{{< tech_preview >}}

Pipelines-as-Code can analyze your CI/CD pipeline runs using Large Language Models (LLMs). An LLM is an AI service (such as OpenAI or Google Gemini) that reads pipeline logs and failure data, then posts human-readable analysis directly on your pull requests.

Use this feature when you want automated root-cause analysis of pipeline failures without manually reading through logs.

## Overview

LLM-powered analysis lets you:

- **Analyze failed pipelines** automatically and get root-cause analysis
- **Generate actionable recommendations** for fixing issues
- **Post insights as PR comments** so your team sees them immediately
- **Configure custom analysis scenarios** using different prompts and triggers

{{< callout type="info" >}}
Additional output destinations (`check-run` and `annotation`) and structured JSON output are planned for future releases.
{{< /callout >}}

## Supported LLM Providers

Pipelines-as-Code supports two LLM providers:

- **OpenAI** -- Default model: `gpt-5.4-mini`
- **Google Gemini** -- Default model: `gemini-3.1-flash-lite-preview`

You can specify any model your chosen provider supports. See [Model Selection]({{< relref "/docs/guides/llm-analysis/model-and-triggers#model-selection" >}}) for guidance on choosing the right model.

## Configuration

To enable LLM-powered analysis, configure the `spec.settings.ai` section in your Repository CR:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/org/repo"
  settings:
    ai:
      enabled: true
      provider: "openai"
      timeout_seconds: 30
      max_tokens: 1000
      secret_ref:
        name: "openai-api-key"
        key: "token"
      roles:
        - name: "failure-analysis"
          model: "gpt-5.4-mini"  # Optional: specify model (uses provider default if omitted)
          prompt: |
            You are a DevOps expert. Analyze this failed pipeline and:
            1. Identify the root cause
            2. Suggest specific fixes
            3. Recommend preventive measures
          on_cel: 'body.pipelineRun.status.conditions[0].reason == "Failed"'
          context_items:
            error_content: true
            container_logs:
              enabled: true
              max_lines: 100
          output: "pr-comment"
```

## Configuration Fields

The following tables describe every field available in the `spec.settings.ai` configuration block.

### Top-Level Settings

| Field             | Type    | Required   | Description                                          |
| ----------------- | ------- | ---------- | ---------------------------------------------------- |
| `enabled`         | boolean | Yes        | Enable/disable LLM analysis                          |
| `provider`        | string  | Yes        | LLM provider: `openai` or `gemini`                   |
| `api_url`         | string  | No         | Custom API endpoint URL (overrides provider default) |
| `timeout_seconds` | integer | No         | Request timeout (1-300, default: 30)                 |
| `max_tokens`      | integer | No         | Maximum response tokens (1-4000, default: 1000)      |
| `secret_ref`      | object  | Yes        | Reference to Kubernetes secret with API key          |
| `roles`           | array   | Yes        | List of analysis scenarios (minimum 1)               |

### Analysis Roles

Each role defines a specific analysis scenario. You can configure multiple roles to handle different types of pipeline events (for example, one role for security failures and another for general build failures).

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | Yes | Unique identifier for this role |
| `prompt` | string | Yes | Prompt template for the LLM |
| `model` | string | No | Model name (consult provider documentation for available models). Uses provider default if not specified. |
| `on_cel` | string | No | CEL expression for conditional triggering. If not specified, the role will always run. |
| `output` | string | Yes | Output destination (currently only `pr-comment` is supported) |
| `context_items` | object | No | Configuration for context inclusion |

### Context Items

Context items control what information Pipelines-as-Code sends to the LLM provider. Choose carefully, because more context means higher token usage and cost.

| Field                      | Type    | Description                                                                  |
| -------------------------- | ------- | ---------------------------------------------------------------------------- |
| `commit_content`           | boolean | Include commit information (see Commit Fields below)                         |
| `pr_content`               | boolean | Include PR title, description, metadata                                      |
| `error_content`            | boolean | Include error messages and failures                                          |
| `container_logs.enabled`   | boolean | Include container/task logs                                                  |
| `container_logs.max_lines` | integer | Limit log lines (1-1000, default: 50). ⚠️ High values may impact performance |

#### Commit Fields

When you set `commit_content: true`, Pipelines-as-Code includes the following fields in the data sent to the LLM provider:

| Field | Type | Description | Example |
| --- | --- | --- | --- |
| `commit.sha` | string | Commit SHA hash | `"abc123def456..."` |
| `commit.message` | string | Commit title (first line/paragraph) | `"feat: add new feature"` |
| `commit.url` | string | Web URL to view the commit | `"https://github.com/org/repo/commit/abc123"` |
| `commit.full_message` | string | Complete commit message (if different from title) | `"feat: add new feature\n\nDetailed description..."` |
| `commit.author.name` | string | Author's name | `"John Doe"` |
| `commit.author.date` | timestamp | When the commit was authored | `"2024-01-15T10:30:00Z"` |
| `commit.committer.name` | string | Committer's name (may differ from author) | `"GitHub"` |
| `commit.committer.date` | timestamp | When the commit was committed | `"2024-01-15T10:31:00Z"` |

**Privacy and Security Notes:**

- Pipelines-as-Code **intentionally excludes email addresses** from the commit context to protect personally identifiable information (PII) when sending data to external LLM providers.
- Fields appear only if your Git provider makes them available. Some providers supply limited information (for example, Bitbucket Cloud provides only the author name).
- Author and committer may be the same person or different (for example, when using `git commit --amend` or rebasing).
asing).
