---
title: "Guides"
weight: 2
sidebar:
  open: true
---

This section covers the core workflows you need to run CI/CD with Pipelines-as-Code. You will find guides on triggering PipelineRuns, configuring your Repository CR, issuing GitOps commands, monitoring pipeline statuses, and validating resources with OpenAPI schemas.

{{< cards >}}
  {{< card link="creating-pipelines" title="Authoring PipelineRuns" subtitle="Create pipelines, CEL variables, GitHub token" >}}
  {{< card link="repository-crd" title="Repository CR" subtitle="Configure repos, concurrency, comment settings, status checks" >}}
  {{< card link="event-matching" title="Event matching" subtitle="on-event, on-target-branch, path, CEL, labels" >}}
  {{< card link="gitops-commands" title="GitOps commands" subtitle="/retest, /test, /cancel and more" >}}
  {{< card link="statuses" title="PipelineRun status" subtitle="Status reporting and failure detection" >}}
  {{< card link="openapi-schema" title="OpenAPI schema" subtitle="Validate Repository CRs in your editor" >}}
  {{< card link="llm-analysis" title="AI/LLM analysis" subtitle="Automated pipeline analysis" >}}
{{< /cards >}}
