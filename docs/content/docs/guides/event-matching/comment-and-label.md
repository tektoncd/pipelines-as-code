---
title: Comment and Label Matching
weight: 2
---

Beyond branch and event annotations, Pipelines-as-Code can trigger PipelineRuns based on comments or labels on pull requests. This is useful when you want human-initiated actions -- such as typing `/merge-pr` in a comment -- to kick off specific pipelines, or when you want labeling a pull request (for example, with `bug`) to automatically run a targeted test suite.

## Matching on a comment regex

{{< tech_preview "Matching PipelineRun on regex in comments" >}}
{{< support_matrix github_app="true" github_webhook="true" forgejo="true" gitlab="true" bitbucket_cloud="false" bitbucket_datacenter="false" >}}

You can trigger a PipelineRun when someone posts a comment on a pull request or a [pushed commit]({{< relref "/docs/guides/gitops-commands/push-commands">}}) by using the annotation `pipelinesascode.tekton.dev/on-comment`.

Pipelines-as-Code treats the annotation value as a regular expression (regex). It strips leading and trailing whitespace from the comment before matching, so `^` matches the start of the comment text and `$` matches the end.

Only newly created comments trigger matching. Edits or updates to existing comments do not trigger the PipelineRun.

Example:

```yaml
---
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: "merge-pr"
  annotations:
    pipelinesascode.tekton.dev/on-comment: "^/merge-pr"
```

Pipelines-as-Code triggers the `merge-pr` PipelineRun when a comment on a pull request starts with `/merge-pr`.

When the `on-comment` annotation triggers a PipelineRun, Pipelines-as-Code sets the template variable {{ trigger_comment }}. For details, see [Accessing the comment triggering the PipelineRun]({{< relref "/docs/guides/gitops-commands/advanced#accessing-the-comment-triggering-the-pipelinerun" >}}).

The `on-comment` annotation follows the pull_request [Policy]({{< relref "/docs/advanced/policy-authorization" >}}) rules. Only users listed in the pull_request policy can trigger the PipelineRun.

```mermaid
flowchart LR
%% title: How a pull request comment is matched against on-comment annotations
  User@{ img: "/icons/user.svg", label: "Commenter", w: 48, h: 48, constraint: "on" }
  PR@{ img: "/icons/comment.svg", label: "New comment", w: 48, h: 48, constraint: "on" }
  Cmd@{ img: "/icons/decision.svg", label: "Built-in?", w: 48, h: 48, constraint: "on" }
  Gops[GitOps command]
  Re@{ img: "/icons/search.svg", label: "on-comment", w: 48, h: 48, constraint: "on" }
  ACL@{ img: "/icons/policy.svg", label: "Access check", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "merge-pr", w: 48, h: 48, constraint: "on" }
  Wait@{ img: "/icons/wait.svg", label: "Pending", w: 48, h: 48, constraint: "on" }
  Drop@{ img: "/icons/stop.svg", label: "Ignored", w: 48, h: 48, constraint: "on" }
  User --> PR --> Cmd
  Cmd -->|yes| Gops
  Cmd -->|no| Re
  Re hit@-->|match| ACL
  Re miss@-->|no match| Drop
  ACL ok@-->|allowed| Run
  ACL denied@-->|denied| Wait

%% play: Custom command
%% skip Gops Drop Wait
%% User -> PR : /merge-pr | Someone posts a new comment on the pull request
%% PR -> Cmd
%% Cmd -> Re | It is not a built-in command, so the on-comment regexes are tried
%% Re -> ACL @hit | The trimmed comment matches ^/merge-pr
%% ACL -> Run @ok | The sender is allowed, so merge-pr starts with {{ trigger_comment }} set
%% play: Built-in command
%% skip Re ACL Run Wait Drop
%% User -> PR : /retest
%% PR -> Cmd
%% Cmd -> Gops | /test, /retest, /cancel and /ok-to-test are handled before any on-comment matching
%% play: Plain comment
%% skip Gops ACL Run Wait
%% User -> PR : LGTM
%% PR -> Cmd
%% Cmd -> Re
%% Re -> Drop @miss | No regex matches, so nothing runs
%% play: Sender not allowed
%% skip Gops Drop Run
%% User -> PR : /merge-pr
%% PR -> Cmd
%% Cmd -> Re
%% Re -> ACL @hit
%% ACL -> Wait @denied | The sender is not allowed to run CI, so nothing starts and the commit gets a pending status
```

{{< callout type="info" >}}
The `on-comment` annotation works with pull_request events. For push events, Pipelines-as-Code supports it only [when targeting the main branch without arguments]({{< relref "/docs/guides/gitops-commands/push-commands" >}}).

Avoid using Pipelines-as-Code's built-in [GitOps commands]({{< relref "/docs/guides/gitops-commands" >}}) — such as `/test`, `/retest`, `/cancel`, or `/ok-to-test` — as `on-comment` patterns. These commands are processed before `on-comment` matching and can lead to unexpected behavior.
{{< /callout >}}

## Matching on pull request labels

{{< tech_preview "Matching PipelineRun to a Pull-Request label" >}}
{{< support_matrix github_app="true" github_webhook="true" forgejo="true" gitlab="true" bitbucket_cloud="false" bitbucket_datacenter="false" >}}

You can use the annotation `pipelinesascode.tekton.dev/on-label` to trigger a PipelineRun when a pull request carries a specific label. For example, to run the PipelineRun `match-bugs-or-defect` whenever a pull request has the label `bug` or `defect`:

```yaml
metadata:
  name: match-bugs-or-defect
  annotations:
    pipelinesascode.tekton.dev/on-label: "[bug, defect]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
```

```mermaid
flowchart LR
%% title: What runs when a label is added and when a commit is pushed to a labeled pull request
  Dev@{ img: "/icons/user.svg", label: "Developer", w: 48, h: 48, constraint: "on" }
  Lab@{ img: "/icons/tag.svg", label: "Label added", w: 48, h: 48, constraint: "on" }
  Push@{ img: "/icons/commit.svg", label: "New commit", w: 48, h: 48, constraint: "on" }
  Check@{ img: "/icons/filter.svg", label: "on-label", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "match-bugs-or-defect", w: 48, h: 48, constraint: "on" }
  Others@{ img: "/icons/pipeline.svg", label: "Other PipelineRuns", w: 48, h: 48, constraint: "on" }
  Dev --> Lab --> Check
  Dev --> Push
  Push --> Check
  Push --> Others
  Check --> Run

%% play: Label added
%% skip Push Others
%% Dev -> Lab : bug | A developer adds the bug label to a pull request targeting main
%% Lab -> Check | Only PipelineRuns with an on-label annotation are considered for a label event
%% Check -> Run | bug is in the list and the branch matches, so only this PipelineRun starts
%% play: New commit
%% skip Lab
%% Dev -> Push : sha | The developer pushes a new commit to the labeled pull request
%% Push -> Check, Others | Every PipelineRun matching the pull request is evaluated
%% Check -> Run | The bug label is still there, so match-bugs-or-defect runs again
```

* The `on-label` annotation respects the `pull_request` [Policy]({{< relref "/docs/advanced/policy-authorization" >}}) rules.
* You still need `on-target-branch` to specify which branch the pull request targets.
* You still need `on-event` to specify the event type.
* Pipelines-as-Code supports this annotation on GitHub, Forgejo, and GitLab only. Bitbucket Cloud and Bitbucket Data Center do not support pull request labels.
* When you add a label to a pull request, Pipelines-as-Code triggers the matching PipelineRun immediately and does not activate other PipelineRuns that match the same pull request.
* If you push a new commit to the pull request, Pipelines-as-Code triggers the label-matched PipelineRun again as long as the label is still present.
* You can access pull request labels with the [dynamic variable]({{< relref "/docs/guides/creating-pipelines#dynamic-variables" >}}) `{{ pull_request_labels }}`. Labels are separated by a Unix newline `\n`. For example, you can print them in a shell script:

  ```bash
   for i in $(echo -e "{{ pull_request_labels }}");do
   echo $i
   done
  ```

## Escaping commas in branch names

If a branch name contains a comma, use the HTML escape entity `&#44;` in place of the literal comma. For example, to match both `main` and a branch named `release,nightly`:

```yaml
pipelinesascode.tekton.dev/on-target-branch: [main, release&#44;nightly]
```
