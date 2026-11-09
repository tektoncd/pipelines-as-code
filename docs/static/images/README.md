# Documentation images

Place screenshot and diagram assets here. They are referenced in the docs as `/images/filename.png`.

## Required assets (referenced in content)

| File                                            | Used in                           |
|-------------------------------------------------|-----------------------------------|
| `tkn-pac-cli.png`                               | CLI Reference                     |
| `gitlab-add-webhook.png`                        | GitLab provider                   |
| `gitlab-pipelines-tab.png`                      | GitLab provider, Comment settings |
| `pac-direct-webhook-create.png`                 | GitHub Webhook provider           |
| `bitbucket-datacenter-create-webhook.png`       | Bitbucket Data Center             |
| `bitbucket-cloud-create-webhook.png`            | Bitbucket Cloud                   |
| `bitbucket-cloud-account-settings.png`          | Bitbucket Cloud                   |
| `git-api-usage-metrics-prometheus-query.png`    | Operations metrics                |
| `snippet-failure-message.png`                   | Statuses guide                    |
| `github-annotation-error-failure-detection.png` | Statuses guide                    |
| `report-error-comment-on-bad-yaml.png`          | Running pipelines                 |
| `github-apps-rerun-checks.png`                  | Running pipelines                 |
| `vscode-openapi-schema.png`                     | OpenAPI schema guide              |
| `gitops-comments-on-commit.png`                 | GitOps push commands              |
| `gitlab-gitops-comment-on-commit.png`           | GitOps push commands              |
| `pr-cancel.png`                                 | GitOps advanced                   |
| `gitops-comments-on-commit-cancel.png`          | GitOps advanced                   |
| `github-app-creation-screen.png`                | Getting started                   |
| `github-app-install-application-on-repo.png`    | Getting started                   |
| `github-app-install-CI-triggered.png`           | Getting started                   |
| `github-app-install-CI-failed.png`              | Getting started                   |
| `github-app-matching-annotations.png`           | Getting started                   |
| `github-app-install-CI-succeeded.png`           | Getting started                   |
| `pac-snazy.png`                                 | Dev setup                         |
| `pac-ecosystem.svg`                             | Tekton Ecosystem                  |
| `pac-ecosystem.png`                             | Tekton Ecosystem                  |

Add PNG (or SVG) files with these names so the documentation renders correctly.
To regenerate `pac-ecosystem.svg` and `pac-ecosystem.png` from `docs/diagrams/assets/`, run `make generate-diagrams`.
