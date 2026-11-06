---
title: "Tekton Ecosystem"
weight: 8
---

Tekton splits CI/CD responsibilities across independent Kubernetes controllers for running containers, signing artifacts, archiving logs, and displaying runs in a web UI. Pipelines-as-Code connects those controllers to your Git provider by reading `.tekton/` definitions from your repository, matching webhook events, creating `PipelineRun` resources, and reporting results back to pull requests and commits.

<a href="/images/pac-ecosystem.svg" target="_blank" rel="noopener"><img src="/images/pac-ecosystem.svg" alt="Pipelines-as-Code in the Tekton Ecosystem"></a>

## Component comparison

| Component | Primary Role | How Pipelines-as-Code Relates |
| --- | --- | --- |
| [Pipelines-as-Code](https://github.com/tektoncd/pipelines-as-code) | Git webhook controller and resolver | Reads `.tekton/` files from Git, matches events, resolves remote tasks, enforces authorization and concurrency, creates `PipelineRun` objects, and reports status to your Git provider. |
| [Tekton Pipelines](https://github.com/tektoncd/pipeline) | Kubernetes execution engine | Executes the `PipelineRun` and `TaskRun` resources that Pipelines-as-Code generates and submits to the cluster. |
| [Tekton Triggers](https://github.com/tektoncd/triggers) | Webhook receiver and template engine | Replaced for Git repositories. Pipelines-as-Code reads `.tekton/` files from Git without requiring `EventListener`, `TriggerBinding`, or `TriggerTemplate` resources. |
| CLI ([`tkn`](https://github.com/tektoncd/cli) & [`tkn pac`]({{< relref "/docs/cli" >}})) | Command-line tooling | The `tkn pac` plugin adds repository bootstrapping, `.tekton/` scaffolding, local YAML resolution, and CEL expression testing to `tkn`. |
| Catalog & Remotes ([Artifact Hub](https://artifacthub.io/), [Tekton Catalog](https://github.com/tektoncd/catalog), [Tekton Hub](https://github.com/tektoncd/hub)) | Reusable `Task` and `Pipeline` definitions | Fetches remote tasks and pipelines from Artifact Hub, Tekton Hub, Git repositories, and HTTP URLs, then inlines them into the `PipelineRun` before submission. |
| UIs & Consoles ([Tekton Dashboard](https://github.com/tektoncd/dashboard), [OpenShift Console](https://github.com/openshift/console)) | Web interface for runs and logs | Formats links to Tekton Dashboard or the OpenShift Console and posts them in pull request checks and status comments. |
| [Tekton Chains](https://github.com/tektoncd/chains) | Artifact signing and provenance | Watches `PipelineRun` and `TaskRun` objects created by Pipelines-as-Code, signs artifacts with [Sigstore](https://www.sigstore.dev/), and writes [in-toto](https://in-toto.io/) provenance attestations. |
| [Tekton Results](https://github.com/tektoncd/results) | Run and log archival | Archives completed run records into a database (and container logs when log storage is configured) so Pipelines-as-Code can prune old `PipelineRun` objects from Kubernetes (`max-keep-runs`). |
| [Tekton Operator](https://github.com/tektoncd/operator) | Cluster installation and upgrades | Installs and upgrades Pipelines-as-Code alongside Tekton Pipelines, Chains, Results, and Dashboard. |
| Observability & AI ([OpenTelemetry](https://opentelemetry.io/), [Prometheus](https://prometheus.io/), [CloudEvents](https://cloudevents.io/)) | Metrics, events, and log analysis | Exports Prometheus and OpenTelemetry metrics and traces (while Tekton Pipelines emits lifecycle CloudEvents), and queries a configured [LLM]({{< relref "/docs/guides/llm-analysis" >}}) to post failure summaries on pull requests. |

## How Pipelines-as-Code compares to other components

### Tekton Pipelines

[Tekton Pipelines](https://tekton.dev/) defines the `Task`, `Pipeline`, `TaskRun`, and `PipelineRun` custom resources and runs container steps in Kubernetes pods. On its own, Tekton Pipelines does not interact with Git repositories, pull requests, webhook signatures, or commit statuses.

Pipelines-as-Code handles the Git side of the workflow. When a webhook arrives from GitHub, GitLab, Forgejo, Gitea, or Bitbucket, Pipelines-as-Code reads the `.tekton/` directory at the event commit revision, evaluates CEL and path filters, queues the run according to concurrency limits, and submits a self-contained `PipelineRun` for Tekton Pipelines to schedule.

### Tekton Triggers

[Tekton Triggers](https://tekton.dev/docs/triggers/) receives arbitrary HTTP webhooks using cluster-side `EventListener`, `TriggerBinding`, `TriggerTemplate`, and `Interceptor` resources. Using Tekton Triggers for repository CI requires maintaining cluster-side YAML templates, payload bindings, and ingress routes for each repository and event type.

Pipelines-as-Code replaces Tekton Triggers for Git workflows. Pipeline definitions stay in `.tekton/*.yaml` inside the application repository instead of cluster-side `TriggerTemplate` objects. Pipelines-as-Code validates webhook payloads, manages short-lived GitHub App tokens, posts check runs and merge request notes, gates untrusted pull requests with `OWNERS` files, and handles `/test`, `/retest`, `/ok-to-test`, and `/cancel` comments.

You do not need Tekton Triggers to run Pipelines-as-Code. Use Tekton Triggers when you need to trigger pipelines from non-Git HTTP sources that cannot use Pipelines-as-Code incoming webhooks.

### Authoring and CLI (`tkn pac` and `tkn`)

The [Tekton CLI (`tkn`)](https://tekton.dev/docs/cli/) inspects and manages cluster resources such as `TaskRun` and `PipelineRun`. The [Pipelines-as-Code CLI (`tkn pac`)]({{< relref "/docs/cli" >}}) runs as a `tkn` plugin or standalone binary for working with `.tekton/` files:

- `tkn pac bootstrap` installs Pipelines-as-Code and configures a GitHub App.
- `tkn pac create repo` and `tkn pac generate` create `Repository` custom resources and starter `.tekton/` pipelines.
- `tkn pac resolve` expands a `.tekton/` template and its remote tasks into a single `PipelineRun` YAML so you can apply it to a cluster before committing.
- `tkn pac cel` evaluates CEL filter expressions against sample webhook payloads, while the [OpenAPI schema]({{< relref "/docs/guides/openapi-schema" >}}) provides autocompletion in your editor.

### Catalog and remote resolvers

You do not need to pre-install shared `Task` objects in the target namespace. Before creating a `PipelineRun`, the [Pipelines-as-Code remote resolver]({{< relref "/docs/guides/pipeline-resolution/remote-pipelines" >}}) reads annotations such as `pipelinesascode.tekton.dev/task` and `pipelinesascode.tekton.dev/pipeline`, fetches the referenced definitions from Artifact Hub, Tekton Hub, remote Git repositories, or HTTP URLs, and inlines them into the `PipelineRun`.

If a pipeline uses Tekton's cluster-side `taskRef.resolver` syntax, Pipelines-as-Code leaves that reference intact so Tekton Pipelines resolves it at runtime.

### Tekton Dashboard and OpenShift Console

Developers see build status first in their Git provider through pull request checks, commit statuses, and comments. When a step fails and a developer needs full container logs, the watcher formats links to Tekton Dashboard or the OpenShift Console (pointing to the failing task step when available) and includes those URLs in GitHub Check Runs, GitLab pipeline statuses, and pull request comments.

### Tekton Chains

[Tekton Chains](https://tekton.dev/docs/chains/) signs artifacts and generates provenance for Tekton workloads without requiring extra configuration in Pipelines-as-Code. After Pipelines-as-Code creates a `PipelineRun` from a verified Git event and Tekton Pipelines finishes running the build, Tekton Chains observes the completed `PipelineRun` and `TaskRun` objects, signs the recorded artifacts with Sigstore (Cosign, Fulcio, and Rekor), and publishes SLSA provenance in in-toto format.

### Tekton Results

Keeping thousands of completed `PipelineRun` resources in Kubernetes increases `etcd` memory use, so Pipelines-as-Code can prune old runs via [`max-keep-runs`]({{< relref "/docs/advanced/cleanup" >}}). [Tekton Results](https://tekton.dev/docs/results/) watches `PipelineRun` and `TaskRun` objects as they complete and copies their run records into an external relational database. Because a default Tekton Results installation stores resource metadata only (`LOGS_API=false`), enable and verify persistent log archival in Tekton Results before relying on `max-keep-runs` if you need to keep pod logs after runs are deleted from the cluster.

### Tekton Operator

[Tekton Operator](https://tekton.dev/docs/operator/) installs and upgrades the Tekton stack on Kubernetes and OpenShift. Enabling Pipelines-as-Code in the `TektonConfig` custom resource tells the operator to deploy and reconcile the Pipelines-as-Code controller, watcher, webhook, `ConfigMap` settings, and OpenShift routes or Kubernetes ingresses.

### Observability, CloudEvents, and AI failure analysis

Pipelines-as-Code and Tekton Pipelines both export [Prometheus and OpenTelemetry metrics]({{< relref "/docs/operations/metrics" >}}) and [distributed traces]({{< relref "/docs/operations/tracing" >}}) for webhook latency, Git API calls, and `PipelineRun` duration. Tekton Pipelines can also emit lifecycle CloudEvents when `TaskRun` and `PipelineRun` resources change state, while Pipelines-as-Code records Kubernetes Events on the target `Repository` namespace. When a `PipelineRun` fails, Pipelines-as-Code can send failed task logs and event context to a configured [LLM]({{< relref "/docs/guides/llm-analysis" >}}) and post a summary of the failure as a pull request comment.
