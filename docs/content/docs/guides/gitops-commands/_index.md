---
title: GitOps Commands
weight: 6
---

GitOps commands let you control Pipelines-as-Code directly from pull request (PR) comments or pushed commits. Use them to retest failures, cancel running pipelines, or trigger specific PipelineRuns without leaving your Git provider's interface.

Because every command lives in the PR comment history, you get a built-in journal of every pipeline execution right next to your code.

## GitOps Commands on Pull Requests

To restart failed PipelineRuns on a PR, add a comment starting with `/retest`. Pipelines-as-Code restarts all **failed** PipelineRuns attached to that PR. If all previous PipelineRuns for the same commit succeeded, Pipelines-as-Code does not create new PipelineRuns, avoiding unnecessary duplication.

Example:

```text
Thanks for contributing. This is a much-needed bugfix, and we appreciate it ❤️ The
failure is not with your PR but seems to be an infrastructure issue.

/retest
```

**What it does:** The `/retest` command creates new PipelineRuns only when:

- Previously **failed** PipelineRuns exist for the same commit, OR
- No PipelineRun has run for the same commit yet

If a successful PipelineRun already exists for the same commit, `/retest` **skips** it to avoid unnecessary duplication. When PipelineRuns have been pruned from the cluster (for example, by a retention policy), Pipelines-as-Code falls back to querying the Git provider's commit status API to determine which pipelines previously succeeded, ensuring only failed pipelines are re-run even when the original PipelineRun objects no longer exist.

**When to use it:** To force a rerun regardless of previous status, use:

```text
/retest <pipelinerun-name>
```

This always triggers a new PipelineRun, even if previous runs succeeded.

Similarly, the `/ok-to-test` command only triggers new PipelineRuns when no successful PipelineRun already exists for the same commit. This prevents duplicate runs when repository owners repeatedly test the same commit with `/test` and `/retest` commands.

### Requiring a SHA with `/ok-to-test`

{{< tech_preview "Requiring a SHA argument to `/ok-to-test`" >}}
{{< support_matrix github_app="true" github_webhook="false" forgejo="false" gitlab="false" bitbucket_cloud="false" bitbucket_datacenter="false" >}}

**What it does:** This setting forces reviewers to include the commit SHA when approving external contributions, closing a timing window that could otherwise let unreviewed code run in CI.

**Why it exists:** GitHub's `issue_comment` event does not include the pull request's HEAD SHA (unlike other Git providers). Without the SHA, an attacker could push a new commit immediately after an owner comments `/ok-to-test`, causing CI to run on unintended code. Requiring the commit ID eliminates this risk.

To enable this protection, cluster administrators set `require-ok-to-test-sha: "true"` in the Pipelines-as-Code ConfigMap. When enabled, repository owners and collaborators must append a 7-40 character Git SHA (in lowercase or uppercase hexadecimal) to the command. For example:

```text
/ok-to-test 1A2B3C4
```

Pipelines-as-Code verifies the provided SHA against the pull request's current HEAD:

- Short SHAs must match the HEAD commit's prefix.
- Full SHAs must match exactly.

If the SHA is missing or invalid, Pipelines-as-Code rejects the comment and replies with instructions to retry using the correct value. Other Git providers already include the commit SHA in their webhook payloads, so this protection applies only to GitHub.

### Targeting Specific PipelineRuns

**What it does:** The `/test` command followed by a PipelineRun name restarts only that specific PipelineRun.

**When to use it:** You have multiple PipelineRuns on a PR and only need to rerun one of them. For example:

```text
Pipeline execution appears to be unstable due to external factors. Retesting this specific pipeline.

/test <pipelinerun-name>
```

{{< callout type="info" >}}
GitOps commands such as `/test` and others do not work on closed pull requests or merge requests.
{{< /callout >}}
