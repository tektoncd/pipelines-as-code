---
title: "Flow Diagrams"
weight: 3
---

This page shows the event flow diagrams for Pipelines-as-Code.

## Diagram of a Pull/Merge Request Flow

[![PAC Diagram](/svg/diagram.svg)](/svg/diagram.svg)

*Detailed sequence from Git event through controller, resolution, and PipelineRun execution. Click to open full size.*

## High-level flow

The controller handles the webhook and creates the PipelineRuns, the watcher follows them to the end. With a concurrency limit on the Repository, the watcher also decides when a queued PipelineRun starts.

```mermaid
sequenceDiagram
%% title: A webhook from the Git provider through the controller, Tekton and the watcher
%% autoplay
  participant Git as Git provider
  participant Ctrl as Controller
  participant K8s as Kubernetes API
  participant Tek as Tekton
  participant W as Watcher
  Git->>Ctrl: Webhook (push / PR)
  Ctrl->>Git: 202 Accepted
  Ctrl->>K8s: Find Repository CR by URL
  Ctrl->>Git: Check sender, fetch .tekton/
  Ctrl->>Ctrl: Match annotations, resolve tasks
  Ctrl->>K8s: Create PipelineRun (pending when limited)
  Ctrl->>Git: Status in progress or queued
  K8s->>W: Queued PipelineRun
  W->>K8s: Start it when a slot is free
  W->>Git: Status in progress
  K8s->>Tek: Run PipelineRun
  Tek->>K8s: Done
  K8s->>W: Finished PipelineRun
  W->>Git: Final status
  W->>K8s: Start next queued, prune old runs

%% play: Pull request
%% Git -> Ctrl : PR #42 | A developer opens a pull request
%% Ctrl -> Git : 202 | The controller answers right away and works in the background
%% Ctrl -> K8s | It finds the Repository CR whose URL matches
%% Ctrl -> Git : .tekton/
%% Ctrl -> Ctrl | Annotations pick the PipelineRuns and remote tasks are inlined
%% Ctrl -> K8s : PipelineRun
%% Ctrl -> Git : running
%% K8s -> Tek
%% K8s -> W : done | The watcher sees the finished PipelineRun
%% W -> Git : success
%% play: Concurrency limit
%% Git -> Ctrl : push
%% Ctrl -> K8s
%% Ctrl -> Git : .tekton/
%% Ctrl -> K8s : pending | The Repository has a concurrency limit, so the PipelineRun is created pending
%% Ctrl -> Git : queued
%% K8s -> W
%% wait 1200 | It waits until a running PipelineRun of the same Repository finishes
%% W -> K8s : start | The watcher takes the next one from the queue and starts it
%% W -> Git : running
%% K8s -> Tek
```
