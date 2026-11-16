---
title: Policy on actions
weight: 5
---

This page explains how to restrict which users can trigger pipelines or approve pull requests for CI. Use policies when you need fine-grained control over who can start PipelineRuns beyond the default collaborator and owner checks.

## Overview

Pipelines-as-Code uses policies to control which actions specific team members can perform
within an organization. You configure these policies in the Repository CR, referencing team names defined on your Git provider (currently GitHub and Forgejo).

{{< support_matrix github_app="true" github_webhook="true" forgejo="true" gitlab="false" bitbucket_cloud="false" bitbucket_datacenter="false" >}}

## Supported actions

* `pull_request` -- Controls who can trigger CI on their own pull requests. When you specify
  a team, only members of that team can trigger CI, regardless
  of whether they are repository or organization owners or
  collaborators. Members listed in the `OWNERS` file can still
  trigger CI.

* `ok_to_test` -- Controls who can authorize CI for external contributors by commenting `/ok-to-test` on a pull request.
  This also applies to `/test` and `/retest`
  commands. Note that `/retest` only re-triggers failed PipelineRuns. This action takes precedence over the `pull_request` action.

## Configuring policies in the Repository CR

To set up policies, add a `settings.policy` block to your Repository CR:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repository1
spec:
  url: "https://github.com/org/repo"
  settings:
    policy:
      ok_to_test:
        - ci-admins
      pull_request:
        - ci-users
```

In this example:

* Members of the `ci-admins` team can authorize other users to run the CI on
  pull requests.
* Members of the `ci-users` team can run CI on their own pull requests.

```mermaid
flowchart LR
%% title: How the pull_request and ok_to_test policies decide who can run CI
%% autoplay
  Ev@{ img: "/icons/event.svg", label: "Event", w: 48, h: 48, constraint: "on" }
  PRT@{ img: "/icons/users.svg", label: "pull_request teams", w: 48, h: 48, constraint: "on" }
  OKT@{ img: "/icons/users.svg", label: "ok_to_test teams", w: 48, h: 48, constraint: "on" }
  Own@{ img: "/icons/document.svg", label: "OWNERS file", w: 48, h: 48, constraint: "on" }
  Run@{ img: "/icons/pipeline.svg", label: "PipelineRuns", w: 48, h: 48, constraint: "on" }
  Ref@{ img: "/icons/refuse.svg", label: "Refused", w: 48, h: 48, constraint: "on" }
  Ev -->|/ok-to-test| OKT
  Ev -->|pull request| PRT
  OKT -->|member| Run
  PRT -->|member| Run
  OKT -->|not a member| Own
  PRT -->|not a member| Own
  Own -->|listed| Run
  Own -->|not listed| Ref

%% play: Team member
%% Ev -> PRT : pull request | A member of ci-users opens a pull request
%% PRT -> Run : member | Team members run CI on their own pull requests
%% play: Owner outside the team
%% Ev -> PRT : pull request | A repository owner who is not in ci-users opens a pull request
%% PRT -> Own : not a member | Being an owner or collaborator is not enough once a policy is set
%% Own -> Ref : not listed | Nothing runs and the pull request waits for approval
%% play: OWNERS entry
%% Ev -> PRT : pull request | A contributor outside ci-users opens a pull request
%% PRT -> Own : not a member
%% Own -> Run : listed | Users listed in the OWNERS file can still run CI
%% play: Approval
%% Ev -> OKT : /ok-to-test | A ci-admins member comments /ok-to-test on an outside contributor's pull request
%% OKT -> Run : member | The ok_to_test teams decide, so the PipelineRuns start
%% play: Approval refused
%% Ev -> OKT : /ok-to-test | A ci-users member comments /ok-to-test
%% OKT -> Own : not a member | The pull_request teams do not count for approvals
%% Own -> Ref : not listed | Nothing runs and the pull request stays pending
```
