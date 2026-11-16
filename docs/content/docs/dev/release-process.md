---
title: Pipelines-as-Code Release Process
---

This page describes the steps to create a new Pipelines-as-Code release.

```mermaid
flowchart LR
%% title: From a pushed tag to a published and announced release
%% autoplay
  Dev@{ img: "/icons/user.svg", label: "Maintainer", w: 48, h: 48, constraint: "on" }
  Tag@{ img: "/icons/tag.svg", label: "v1.2.3", w: 48, h: 48, constraint: "on" }
  GH@{ img: "/icons/repository.svg", label: "GitHub", w: 48, h: 48, constraint: "on" }
  CI@{ img: "/icons/pipeline.svg", label: "release-pipeline", w: 48, h: 48, constraint: "on" }
  Branches[Release branches]
  Gorel@{ img: "/icons/build.svg", label: "goreleaser", w: 48, h: 48, constraint: "on" }
  Img@{ img: "/icons/image.svg", label: "ghcr.io images", w: 48, h: 48, constraint: "on" }
  Rel@{ img: "/icons/release.svg", label: "GitHub release", w: 48, h: 48, constraint: "on" }
  Notes@{ img: "/icons/document.svg", label: "Release notes", w: 48, h: 48, constraint: "on" }
  Ann@{ img: "/icons/notification.svg", label: "Announce", w: 48, h: 48, constraint: "on" }
  Dev --> Tag --> GH
  GH -->|push event| CI
  GH -->|Actions| Img
  CI --> Branches --> Gorel -->|pre-release| Rel
  Dev --> Notes --> Rel --> Ann

%% play: Build the release
%% skip Notes Ann
%% Dev -> Tag | Tag the release locally
%% Tag -> GH : push | and push the tag to tektoncd/pipelines-as-code
%% GH -> CI, Img | PAC runs release-pipeline on the CI cluster while GitHub Actions builds the images with ko
%% CI -> Branches : release.yaml | release.yaml is generated and pushed to release-v1.2.3, release-v1.2.x and stable when it is the newest
%% wait 1000 | goreleaser takes a while
%% Branches -> Gorel
%% Gorel -> Rel : v1.2.3 | Binaries and the release YAML land on a GitHub pre-release
%% play: Publish the release
%% skip Tag GH CI Branches Gorel Img
%% Dev -> Notes : /release-notes | Draft the notes from the merged pull requests and review them
%% Notes -> Rel | Update the GitHub release
%% Rel -> Ann : Slack | Announce it on Slack and social media
```

## Prerequisites

* Clear out any PRs that need to be merged.
* Wait for CI to complete.
* Verify the PAC CI cluster is up.
* Verify that you have GPG signing [set up](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification) for your commits.

## Tagging the Release

Choose between a major, minor, or patch release version.

For example, to release version 1.2.3, tag it locally:

```shell
git tag v1.2.3
```

Push it directly to the repository (you need write access):

```shell
% git push --no-verify git@github.com:tektoncd/pipelines-as-code refs/tags/v1.2.3
```

## Monitoring the Release

Once the tag is pushed, follow the release pipeline on the PAC cluster:

`tkn pr logs -n pipelines-as-code-ci -Lf`

After a while (gorelease takes some time), the new version should appear as a pre-release at:

<https://github.com/tektoncd/pipelines-as-code/releases>

## Publishing the Release

Using claude (or others) skills you can do `/release-notes` to draft the
release notes it will guide thru the release notes generation and update the
release notes in the GitHub release page and a Slack announcement. The release
notes are generated based on the merged PRs since the last release, so it may
not be perfect, so you should always:

* Verify the content, as it may contain mistakes.
* Avoid overusing emojis. Keep the tone professional.
* Categorize changes properly. AI may sometimes expose internal changes as major features.

Announce the release on Slack (upstream/downstream) and Twitter.

## Troubleshooting

If you need to re-trigger the release process due to system or other issues:

```shell
   git tag --force v1.2.3
   git push --force git@github.com:tektoncd/pipelines-as-code v1.2.3
```

Common issues:

* The GitHub token may be expired or badly generated with a trailing `\n`.
* If you did not run `git fetch -a origin` before tagging, you may not have the latest commits from `origin/main`.
