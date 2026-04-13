# Release Notes Format Reference

## Entry Format

Each release note entry MUST follow this exact format. The Link and Jira lines MUST be indented with two spaces so they render as nested sub-bullets:

```markdown
* **Bold title:** One-sentence description of the change.
  * Link: <PR_OR_COMMIT_URL>
  * Jira: [SRVKP-XXXX](https://issues.redhat.com/browse/SRVKP-XXXX)
```

### Rules

- The first bullet MUST start with `*` (no indent) with a bold title followed by a colon and a description.
- The Link line MUST start with `* Link:` (two-space indent) with the PR or commit URL.
- The Jira line MUST start with `* Jira:` (two-space indent). Include it ONLY if the entry has JIRA tickets. If there are multiple tickets, list each as a separate markdown link comma-separated.
- Within each section, list entries that have JIRA tickets FIRST, before entries without JIRA tickets.
- Do NOT add a Contributors section.

## Header Template

```markdown
# Pipelines as Code version {tag}

OpenShift Pipelines as Code {tag} has been released 🥳
```

## Section Headers

Use exactly these section headers (skip empty ones):

```markdown
## ✨ Major changes and Features
## 🐛 Bug Fixes
## 📚 Documentation Updates
## ⚙️ Chores
```

## Installation Section Template

```markdown
## Installation

To install this version you can install the release.yaml with [`kubectl`](https://kubernetes.io/docs/tasks/tools/) for your platform :

### Openshift

\`\`\`shell
kubectl apply -f https://github.com/{owner}/{repo}/releases/download/{tag}/release.yaml
\`\`\`

### Kubernetes

\`\`\`shell
kubectl apply -f https://github.com/{owner}/{repo}/releases/download/{tag}/release.k8s.yaml
\`\`\`

### Documentation

The documentation for this release is available here :

https://docs.pipelinesascode.com/{tag}
```

Where `{tag_dashed}` is the tag with dots replaced by dashes (e.g., `v0.31.0` → `v0-31-0`).

## JIRA Ticket Format

JIRA tickets matching `SRVKP-\d+` should be linked as:

```markdown
[SRVKP-XXXX](https://issues.redhat.com/browse/SRVKP-XXXX)
```

Multiple tickets are comma-separated:

```markdown
[SRVKP-1234](https://issues.redhat.com/browse/SRVKP-1234), [SRVKP-5678](https://issues.redhat.com/browse/SRVKP-5678)
```

## Complete Example

```markdown
# Pipelines as Code version v0.31.0

OpenShift Pipelines as Code v0.31.0 has been released 🥳

## ✨ Major changes and Features

* **Cache changed files in Gitea provider:** Improved performance by caching changed files in the Gitea provider, reducing API calls during pipeline runs.
  * Link: https://github.com/openshift-pipelines/pipelines-as-code/pull/2145
  * Jira: [SRVKP-1234](https://issues.redhat.com/browse/SRVKP-1234)
* **Add concurrency support for pipeline runs:** Introduced concurrency controls allowing users to limit parallel pipeline executions per repository.
  * Link: https://github.com/openshift-pipelines/pipelines-as-code/pull/2130

## 🐛 Bug Fixes

* **Pin actions/checkout to a specific hash:** Fixed CI reliability by pinning the checkout action to a known-good commit hash.
  * Link: https://github.com/openshift-pipelines/pipelines-as-code/pull/2140
  * Jira: [SRVKP-5678](https://issues.redhat.com/browse/SRVKP-5678)

## ⚙️ Chores

* **Bump actions/download-artifact from 7.0.0 to 8.0.0:** Updated CI dependency to latest version.
  * Link: https://github.com/openshift-pipelines/pipelines-as-code/pull/2138

## Installation

To install this version you can install the release.yaml with [`kubectl`](https://kubernetes.io/docs/tasks/tools/) for your platform :

### Openshift

\`\`\`shell
kubectl apply -f https://github.com/openshift-pipelines/pipelines-as-code/releases/download/v0.31.0/release.yaml
\`\`\`

### Kubernetes

\`\`\`shell
kubectl apply -f https://github.com/openshift-pipelines/pipelines-as-code/releases/download/v0.31.0/release.k8s.yaml
\`\`\`

### Documentation

The documentation for this release is available here :

https://docs.pipelinesascode.com/v0.31.0

## What's Changed
<!-- GitHub auto-generated changelog goes here -->
```
