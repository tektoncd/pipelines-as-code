---
title: Model Selection and Triggers
weight: 1
---

This page explains how to choose the right LLM model for each analysis role and how to use CEL expressions to control when Pipelines-as-Code triggers analysis. Use model selection to balance cost and capability, and use CEL triggers to limit analysis to the events that matter.

## Model Selection

Each analysis role can specify a different model. Choosing the right model lets you balance cost against analysis depth. If you do not specify a model, Pipelines-as-Code uses provider-specific defaults:

- **OpenAI**: `gpt-5.4-mini`
- **Gemini**: `gemini-3.1-flash-lite-preview`

### Specifying Models

You can use any model name that your chosen LLM provider supports. Consult the provider's documentation for available models:

- **OpenAI Models**: <https://platform.openai.com/docs/models>
- **Gemini Models**: <https://ai.google.dev/gemini-api/docs/models/gemini>

### Example: Assigning Different Models per Role

The following example shows how to assign a different model to each analysis role, matching the model's capability to the task's complexity:

```yaml
settings:
  ai:
    enabled: true
    provider: "openai"
    secret_ref:
      name: "openai-api-key"
      key: "token"
    roles:
      # Use the most capable model for complex analysis
      - name: "security-analysis"
        model: "gpt-5"
        prompt: "Analyze security failures..."

      # Use default model (gpt-5.4-mini) for general analysis
      - name: "general-failure"
        # No model specified - uses provider default
        prompt: "Analyze this failure..."

      # Use the most economical model for quick checks
      - name: "quick-check"
        model: "gpt-5-nano"
        prompt: "Quick diagnosis..."
```

## CEL Expressions for Triggers

By default, Pipelines-as-Code runs LLM analysis only for failed PipelineRuns. CEL (Common Expression Language) expressions in the `on_cel` field let you refine this behavior -- for example, restricting analysis to a specific branch or enabling it for successful runs too.

If you omit `on_cel`, the role executes for all failed PipelineRuns.

### Overriding the Default Behavior

To run analysis for **all PipelineRuns** (both successful and failed), set `on_cel: 'true'`:

```yaml
roles:
  - name: "pipeline-summary"
    prompt: "Generate a summary of this pipeline run..."
    on_cel: 'true'  # Runs for ALL pipeline runs, not just failures
    output: "pr-comment"
```

This is useful when you want to:

- Generate summaries for every PipelineRun
- Track metrics for successful runs
- Post automated messages on successful builds
- Report on build performance

### Example CEL Expressions

```yaml
# Run on ALL pipeline runs (overrides default failed-only behavior)
on_cel: 'true'

# Only on successful runs (e.g., for generating success reports)
on_cel: 'body.pipelineRun.status.conditions[0].reason == "Succeeded"'

# Only on pull requests (in addition to default failed-only check)
on_cel: 'body.event.event_type == "pull_request"'

# Only on main branch
on_cel: 'body.event.base_branch == "main"'

# Only on default branch (works across repos with different default branches)
on_cel: 'body.event.base_branch == body.event.default_branch'

# Skip analysis for bot users
on_cel: 'body.event.sender != "dependabot[bot]"'

# Only for PRs with specific labels
on_cel: '"needs-review" in body.event.pull_request_labels'

# Only when triggered by comment
on_cel: 'body.event.trigger_comment.startsWith("/analyze")'

# Combine conditions
on_cel: 'body.pipelineRun.status.conditions[0].reason == "Failed" && body.event.event_type == "pull_request"'
```

### Available CEL Context Fields

The following tables list all fields you can reference in `on_cel` expressions. Pipelines-as-Code populates these fields from the PipelineRun status, the Repository CR, and the Git provider event.

#### Top-Level Context

| Field              | Type              | Description                                      |
| ------------------ | ----------------- | ------------------------------------------------ |
| `body.pipelineRun` | object            | Full PipelineRun object with status and metadata |
| `body.repository`  | object            | Full Repository CR object                        |
| `body.event`       | object            | Event information (see Event Fields below)       |
| `pac`              | map[string]string | PAC parameters map                               |

#### Event Fields (`body.event.*`)

**Event Type and Trigger:**

| Field            | Type   | Description                              | Example                                            |
| ---------------- | ------ | ---------------------------------------- | -------------------------------------------------- |
| `event_type`     | string | Event type from provider                 | `"pull_request"`, `"push"`, `"Merge Request Hook"` |
| `trigger_target` | string | Normalized trigger type across providers | `"pull_request"`, `"push"`                         |

**Branch and Commit Information:**

| Field            | Type   | Description                               | Example                   |
| ---------------- | ------ | ----------------------------------------- | ------------------------- |
| `sha`            | string | Commit SHA                                | `"abc123def456..."`       |
| `sha_title`      | string | Commit title/message                      | `"feat: add new feature"` |
| `base_branch`    | string | Target branch for PR (or branch for push) | `"main"`                  |
| `head_branch`    | string | Source branch for PR (or branch for push) | `"feature-branch"`        |
| `default_branch` | string | Default branch of the repository          | `"main"` or `"master"`    |

**Repository Information:**

| Field          | Type   | Description             | Example     |
| -------------- | ------ | ----------------------- | ----------- |
| `organization` | string | Organization/owner name | `"my-org"`  |
| `repository`   | string | Repository name         | `"my-repo"` |

**URLs:**

| Field      | Type   | Description            | Example                                       |
| ---------- | ------ | ---------------------- | --------------------------------------------- |
| `url`      | string | Web URL to repository  | `"https://github.com/org/repo"`               |
| `sha_url`  | string | Web URL to commit      | `"https://github.com/org/repo/commit/abc123"` |
| `base_url` | string | Web URL to base branch | `"https://github.com/org/repo/tree/main"`     |
| `head_url` | string | Web URL to head branch | `"https://github.com/org/repo/tree/feature"`  |

**User Information:**

| Field    | Type   | Description                  | Example                          |
| -------- | ------ | ---------------------------- | -------------------------------- |
| `sender` | string | User who triggered the event | `"user123"`, `"dependabot[bot]"` |

**Pull Request Fields (only populated for PR events):**

| Field                 | Type     | Description          | Example                           |
| --------------------- | -------- | -------------------- | --------------------------------- |
| `pull_request_number` | int      | PR/MR number         | `42`                              |
| `pull_request_title`  | string   | PR/MR title          | `"Add new feature"`               |
| `pull_request_labels` | []string | List of PR/MR labels | `["enhancement", "needs-review"]` |

**Comment Trigger Fields (only when triggered by comment):**

| Field             | Type   | Description                    | Example                |
| ----------------- | ------ | ------------------------------ | ---------------------- |
| `trigger_comment` | string | Comment that triggered the run | `"/test"`, `"/retest"` |

**Webhook Fields:**

| Field                | Type   | Description                              | Example             |
| -------------------- | ------ | ---------------------------------------- | ------------------- |
| `target_pipelinerun` | string | Target PipelineRun for incoming webhooks | `"my-pipeline-run"` |

#### Excluded Fields

Pipelines-as-Code **intentionally excludes** the following fields from the CEL context for security and architectural reasons:

- **`event.Provider`** -- Contains sensitive API tokens and webhook secrets.
- **`event.Request`** -- Contains raw HTTP headers and payload, which may include secrets.
- **`event.InstallationID`**, **`AccountID`**, **`GHEURL`**, **`CloneURL`** -- Provider-specific internal identifiers and URLs.
- **`event.SourceProjectID`**, **`TargetProjectID`** -- GitLab-specific internal identifiers.
- **`event.State`** -- Internal state management fields.
- **`event.Event`** -- Raw provider event object (already represented in the structured fields above).

### Output Destinations

Output destinations control where Pipelines-as-Code posts the LLM analysis results.

#### PR Comment

Posts analysis as a comment on the pull request:

```yaml
output: "pr-comment"
```

Benefits of PR comments:

- Visible to all developers working on the pull request.
- Pipelines-as-Code can update the comment with new analysis on subsequent runs.
- Easy to discuss and follow up on directly in the PR conversation.

{{< callout type="info" >}}
Additional output destinations including `check-run` (GitHub check runs) and `annotation` (PipelineRun annotations) are planned for future releases.
{{< /callout >}}
