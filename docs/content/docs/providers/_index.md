---
title: "Git Providers"
weight: 3
sidebar:
  open: true
---

Configure Pipelines-as-Code with your Git provider: GitHub, GitLab, Bitbucket, or Forgejo.

{{< cards >}}
  {{< card link="github-app" title="GitHub Apps" subtitle="Recommended: GitHub App integration" >}}
  {{< card link="github-webhook" title="GitHub Webhook" subtitle="Webhook-based GitHub setup" >}}
  {{< card link="gitlab" title="GitLab" subtitle="GitLab.com and self-hosted" >}}
  {{< card link="bitbucket-cloud" title="Bitbucket Cloud" subtitle="Bitbucket.org" >}}
  {{< card link="bitbucket-datacenter" title="Bitbucket Data Center" subtitle="Self-hosted Bitbucket Data Center" >}}
  {{< card link="forgejo" title="Forgejo" subtitle="Forgejo and Gitea" >}}
{{< /cards >}}

Every provider sends events to the same controller, but each one proves differently that an event really comes from it. Pick your provider in the story list to see which check applies.

```mermaid
flowchart LR
%% title: How the controller verifies a webhook from each Git provider
  Git@{ img: "/icons/repository.svg", label: "Git provider", w: 48, h: 48, constraint: "on" }
  Ctrl@{ img: "/icons/controller.svg", label: "Controller", w: 48, h: 48, constraint: "on" }
  AppSec@{ img: "/icons/key.svg", label: "Controller secret", w: 48, h: 48, constraint: "on" }
  Repo@{ img: "/icons/config.svg", label: "Repository CR", w: 48, h: 48, constraint: "on" }
  RepoSec@{ img: "/icons/secret.svg", label: "Repository secret", w: 48, h: 48, constraint: "on" }
  IPs@{ img: "/icons/network.svg", label: "Atlassian IP ranges", w: 48, h: 48, constraint: "on" }
  Check@{ img: "/icons/shield.svg", label: "Verify", w: 48, h: 48, constraint: "on" }
  Ok@{ img: "/icons/success.svg", label: "Processed", w: 48, h: 48, constraint: "on" }
  No@{ img: "/icons/refuse.svg", label: "Refused", w: 48, h: 48, constraint: "on" }
  Git -->|webhook| Ctrl
  Ctrl --> AppSec --> Check
  Ctrl --> Repo --> RepoSec --> Check
  Ctrl --> IPs --> Check
  Check ok@-->|valid| Ok
  Check bad@-->|invalid| No

%% play: GitHub App
%% skip IPs Repo RepoSec No
%% Git -> Ctrl : event | GitHub signs the payload in the X-Hub-Signature-256 header
%% Ctrl -> AppSec : webhook.secret | The secret comes from pipelines-as-code-secret in the controller namespace
%% AppSec -> Check : HMAC
%% Check -> Ok @ok : token | Only then does the controller mint an installation token with the App private key
%% play: GitHub webhook
%% skip AppSec IPs No
%% Git -> Ctrl : event
%% Ctrl -> Repo | The controller first finds the Repository CR that matches the repository URL
%% Repo -> RepoSec : webhook.secret | It reads provider.token and webhook.secret from the secret the Repository CR names
%% RepoSec -> Check : HMAC | X-Hub-Signature-256 must match, a payload with no signature is refused
%% Check -> Ok @ok
%% play: GitLab
%% skip AppSec IPs No
%% Git -> Ctrl : event
%% Ctrl -> Repo
%% Repo -> RepoSec : webhook.secret
%% RepoSec -> Check : X-Gitlab-Token | GitLab sends the secret itself in the X-Gitlab-Token header and the controller compares it as is
%% Check -> Ok @ok
%% play: Bitbucket Cloud
%% skip AppSec Repo RepoSec No
%% Git -> Ctrl : event
%% Ctrl -> IPs : source IP | The last address in X-Forwarded-For must be in the Atlassian IP ranges or the extra ranges you configure
%% IPs -> Check
%% Check -> Ok @ok | With the IP check on, which is the default, no signature is checked
%% play: Bitbucket Data Center
%% skip AppSec IPs No
%% Git -> Ctrl : event
%% Ctrl -> Repo
%% Repo -> RepoSec : webhook.secret
%% RepoSec -> Check : HMAC | The X-Hub-Signature header must match the webhook secret
%% Check -> Ok @ok
%% play: Forgejo
%% skip AppSec IPs No
%% Git -> Ctrl : event
%% Ctrl -> Repo
%% Repo -> RepoSec : webhook.secret | Forgejo needs a webhook secret, an empty one is refused
%% RepoSec -> Check : HMAC-SHA256 | The X-Forgejo-Signature or X-Gitea-Signature header must match
%% Check -> Ok @ok
%% play: Wrong secret
%% skip AppSec IPs Ok
%% Git -> Ctrl : event
%% Ctrl -> Repo
%% Repo -> RepoSec : webhook.secret
%% RepoSec -> Check
%% Check -> No @bad | The event stops here, nothing is fetched and no PipelineRun starts
```
