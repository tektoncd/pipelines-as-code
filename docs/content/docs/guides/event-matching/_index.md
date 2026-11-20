---
title: Matching a PipelineRun
weight: 5
---

Event matching is the mechanism Pipelines-as-Code uses to decide which PipelineRuns to execute in response to Git provider activity. Every time a pull request opens, a commit pushes, a tag is created, or a tag release is released, Pipelines-as-Code inspects the annotations on each PipelineRun in your `.tekton/` directory and runs only the ones whose criteria match the incoming event. This page covers the two core annotations -- `on-event` and `on-target-branch` -- that control basic matching. For more advanced filtering, see [CEL Expressions]({{< relref "/docs/guides/event-matching/cel-expressions" >}}), [Path-Based Matching]({{< relref "/docs/guides/event-matching/path-matching" >}}), and [Comment and Label Matching]({{< relref "/docs/guides/event-matching/comment-and-label" >}}).

```mermaid
flowchart LR
%% title: How an event is matched to the PipelineRuns in .tekton/
%% autoplay
  Ev@{ img: "/icons/webhook.svg", label: "Event", w: 48, h: 48, constraint: "on" }
  Tek@{ img: "/icons/folder.svg", label: ".tekton/", w: 48, h: 48, constraint: "on" }
  Com@{ img: "/icons/comment.svg", label: "on-comment", w: 48, h: 48, constraint: "on" }
  Cel@{ img: "/icons/code.svg", label: "CEL", w: 48, h: 48, constraint: "on" }
  Ann@{ img: "/icons/filter.svg", label: "Event and branch", w: 48, h: 48, constraint: "on" }
  Extra[Paths and labels]
  Run@{ img: "/icons/pipeline.svg", label: "PipelineRuns", w: 48, h: 48, constraint: "on" }
  Drop@{ img: "/icons/stop.svg", label: "Not started", w: 48, h: 48, constraint: "on" }
  Ev --> Tek
  Tek --> Com
  Tek --> Cel
  Tek --> Ann
  Ann hit@-->|match| Extra
  Ann miss@-->|no match| Drop
  Extra --> Run
  Cel celok@-->|true| Run
  Cel celno@-->|false| Drop
  Com --> Run

%% play: Pull request
%% skip Com Cel Drop
%% Ev -> Tek : PR to main | A pull request targeting main arrives
%% Tek -> Ann | Each PipelineRun is checked against its on-event and on-target-branch annotations
%% Ann -> Extra @hit : main
%% Extra -> Run | Every PipelineRun that matches starts, in parallel
%% play: Push
%% skip Com Cel Drop
%% Ev -> Tek : refs/heads/main | A commit is pushed to main
%% Tek -> Ann | on-event: [push] and on-target-branch: [main] match the short or the full ref
%% Ann -> Extra @hit
%% Extra -> Run
%% play: Comment
%% skip Cel Ann Extra Drop
%% Ev -> Tek : /merge-pr | Someone comments on a pull request
%% Tek -> Com | Only the on-comment annotations are checked for a comment
%% Com -> Run | The PipelineRun whose regex matches the comment starts
%% play: CEL expression
%% skip Com Ann Extra
%% Ev -> Tek : PR to main
%% Tek -> Cel | A PipelineRun with on-cel-expression ignores its other matching annotations
%% Cel -> Run @celok
%% play: No match
%% skip Com Cel Extra Run
%% Ev -> Tek : push to dev
%% Tek -> Ann
%% Ann -> Drop @miss | No PipelineRun targets dev, so nothing runs
```

## Matching by event and branch

You match a PipelineRun to an event by adding annotations to its metadata. For example:

```yaml
metadata:
  name: pipeline-pr-main
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[main]"
  pipelinesascode.tekton.dev/on-event: "[pull_request]"
```

Pipelines-as-Code matches the PipelineRun `pipeline-pr-main` when a `pull_request` event targets the `main` branch.

You can specify multiple target branches by separating them with commas:

```yaml
pipelinesascode.tekton.dev/on-target-branch: [main, release-nightly]
```

## Matching push events

Besides `pull_request` events, you can also match PipelineRuns on `push` events. For example, the following PipelineRun runs whenever you push a commit to the `main` branch:

```yaml
metadata:
  name: pipeline-push-on-main
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[refs/heads/main]"
    pipelinesascode.tekton.dev/on-event: "[push]"
```

You can specify the full ref like `refs/heads/main` or the short form like `main`. Glob patterns also work -- for example, `refs/heads/*` matches any branch and `refs/tags/1.*` matches all tags starting with `1.`.

## Matching tag pushes

The following example matches when you push the `1.0` tag:

```yaml
metadata:
name: pipeline-push-on-1.0-tags
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[refs/tags/1.0]"
  pipelinesascode.tekton.dev/on-event: "[push]"
```

Pipelines-as-Code runs the PipelineRun `pipeline-push-on-1.0-tags` when you push the `1.0` tag to your repository.

{{< callout type="warning" >}}
GitHub does not send webhook events when more than three tags are pushed simultaneously (e.g., with `git push origin --tags`). To ensure PipelineRuns are triggered for all tags, push them in batches of three or fewer. [See GitHub's docs here](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#create).
{{< /callout >}}

## Matching release events

{{< tech_preview "Release Event" >}}

{{< support_matrix github_app="true" github_webhook="true" forgejo="false" gitlab="false" bitbucket_cloud="false" bitbucket_datacenter="false" >}}

You can trigger PipelineRuns when a GitHub release is released. Use `release` as the event type and a tag pattern in `on-target-branch`:

```yaml
metadata:
  name: pipeline-on-release
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[refs/tags/*]"
    pipelinesascode.tekton.dev/on-event: "[release]"
```

Pipelines-as-Code runs this PipelineRun whenever a release is released for any tag. You can narrow the match to specific tags using glob patterns -- for example, `refs/tags/v1.*` matches only tags starting with `v1.`.

Both annotated tags and lightweight tags are supported. Pipelines-as-Code resolves the tag to its underlying commit SHA so that the PipelineRun runs against the correct source.

Only the `released` action triggers a PipelineRun. Draft releases, pre-releases, and other release actions (such as `created`, `edited`, or `deleted`) do not trigger a match.

## Required annotations and parallel execution

Matching annotations are required; without them, Pipelines-as-Code does not match your PipelineRun. When multiple PipelineRuns match the same event, Pipelines-as-Code runs them in parallel and posts each result to the Git provider as soon as the PipelineRun finishes.

{{< callout type="info" >}}

* Pipelines-as-Code matches payloads only for supported events, such as when a pull request opens or updates, when a branch receives a push, or when a tag release is released.

* You typically need both `on-target-branch` and `on-event` annotations for a match, except when you use [CEL expressions]({{< relref "/docs/guides/event-matching/cel-expressions" >}}) or [comment-based matching]({{< relref "/docs/guides/event-matching/comment-and-label" >}}).
{{< /callout >}}
