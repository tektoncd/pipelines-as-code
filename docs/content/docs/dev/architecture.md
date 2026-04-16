---
title: "Architecture"
weight: 2
---

Pipelines-as-Code (PAC) is built on top of Tekton Pipelines and provides a GitOps-native CI/CD experience. This page explains the architecture, components, and how they work together.

## Architecture Overview

PAC consists of three main components that work together to process Git events and manage pipeline executions:

## Core Components

### Controller

**Binary**: `pipelines-as-code-controller`
**Responsibilities**:

- Receives webhook events from Git providers
- Validates event authenticity and permissions
- Resolves pipeline definitions from `.tekton/` directory
- Creates PipelineRuns on the Kubernetes cluster
- Manages concurrency and queueing
- Handles GitOps commands (`/test`, `/retest`, `/cancel`)

**Key Packages**:

- `pkg/adapter`: Event processing and webhook handling
- `pkg/matcher`: Matches events against pipeline annotations
- `pkg/provider`: Git provider integrations (GitHub, GitLab, etc.)
- `pkg/acl`: Access control and permission validation
- `pkg/pipelineascode`: Core pipeline processing logic

**Location**: `cmd/pipelines-as-code-controller/main.go`

### Watcher

**Binary**: `pipelines-as-code-watcher`
**Responsibilities**:

- Watches PipelineRun resources for status changes
- Reports status back to Git providers (checks, comments, badges)
- Manages PipelineRun cleanup based on retention policies
- Handles concurrency queue processing
- Updates Repository CR status

**Key Packages**:

- `pkg/reconciler`: PipelineRun reconciliation logic
- `pkg/kubeinteraction`: Kubernetes API interactions
- `pkg/formatting`: Status formatting for different providers
- `pkg/queue`: Concurrency queue management

**Location**: `cmd/pipelines-as-code-watcher/main.go`

### Webhook Handler

**Binary**: `pipelines-as-code-webhook` (embedded in controller)
**Responsibilities**:

- Receives HTTP webhook requests from Git providers
- Validates webhook signatures
- Routes events to the controller
- Handles incoming webhook feature for manual triggers

**Location**: `cmd/pipelines-as-code-webhook/main.go`

## Event Flow

The following describes how a typical pull request event flows through the system:

1

Git Event

A developer opens a pull request on GitHub, GitLab, or another supported Git provider.

2

Webhook Delivery

The Git provider sends a webhook HTTP POST request to the PAC webhook handler.

3

Event Validation

The webhook handler:

- Verifies the webhook signature
- Validates the event payload
- Checks for skip CI markers (`[skip ci]`, `[ci skip]`)

4

Repository Lookup

The controller:

- Finds the matching Repository CR
- Retrieves authentication credentials
- Validates the repository configuration

5

Permission Check

The ACL system validates:

- User is authorized (org member, collaborator, or in OWNERS file)
- Pull request doesn’t require `/ok-to-test` approval
- Event matches allowed event types

6

Pipeline Resolution

The resolver:

- Fetches `.tekton/` directory from the repository
- Matches pipelines to the event using annotations
- Resolves remote tasks from Tekton Hub or Artifact Hub
- Substitutes template variables (`{{repo_url}}`, `{{revision}}`, etc.)

7

PipelineRun Creation

The controller creates a PipelineRun resource on Kubernetes with:

- Resolved pipeline definition
- Workspaces and secrets
- Labels and annotations for tracking

8

Execution

Tekton Pipelines executes the PipelineRun:

- Creates pods for each task
- Runs the pipeline steps
- Manages task dependencies and workspaces

9

Status Monitoring

The watcher:

- Monitors PipelineRun status changes
- Reports progress to the Git provider
- Updates GitHub Checks, GitLab MR notes, etc.

10

Completion

When the PipelineRun completes:

- Final status is reported to the Git provider
- Logs and artifacts are linked in comments
- Repository CR status is updated
- Cleanup is scheduled based on retention policy

## Key Design Patterns

### Repository CR

The `Repository` Custom Resource (CR) is the central configuration object:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
  namespace: my-namespace
spec:
  url: "https://github.com/org/repo"
  git_provider:
    type: "github"
    secret:
      name: "github-token"
      key: "token"
  settings:
    policy: "allowed"
```

**Purpose**:

- Maps Git repositories to Kubernetes namespaces
- Stores authentication credentials
- Configures provider-specific settings
- Tracks PipelineRun history and status

### Event Matching

Pipelines are matched to events using annotations:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main, develop]"
    pipelinesascode.tekton.dev/on-cel-expression: |
      event == "pull_request" && source_branch.startsWith("feature/")
```

**Matching Logic**:

1. Check `on-event` annotation matches event type
2. Verify `on-target-branch` matches target branch
3. Evaluate `on-cel-expression` if present
4. Check `on-path-change` if file patterns specified

**Package**: `pkg/matcher`

### Pipeline Resolution

The resolver processes pipeline definitions:
**Steps**:

1. Fetch `.tekton/` directory from the repository at the event revision
2. Parse YAML files for PipelineRun resources
3. Match pipelines to the event
4. Resolve remote tasks using `resolver` field:

   ```yaml
   taskRef:
     name: git-clone
     resolver: hub  # or bundles
   ```

5. Substitute template variables:
   - `{{repo_url}}` → Repository clone URL
   - `{{revision}}` → Git SHA
   - `{{target_branch}}` → PR target branch
   - `{{source_branch}}` → PR source branch

**Package**: `pkg/pipelineascode/resolve`

### Access Control (ACL)

PAC implements a flexible ACL system:
**Permission Levels**:

1. **Repository admins**: Full access
2. **Organization members**: Can trigger pipelines
3. **Collaborators**: Can trigger pipelines if configured
4. **OWNERS file**: Custom approval rules
5. **`/ok-to-test`**: Manual approval for untrusted PRs

**Configuration**:

```yaml
# Repository CR
spec:
  settings:
    policy: "allowed"  # allowed, required (OWNERS), denied
```

**Package**: `pkg/acl`

### Concurrency Control

PAC manages pipeline execution concurrency:
**Annotation**:

```yaml
metadata:
  annotations:
    pipelinesascode.tekton.dev/max-concurrency: "1"
```

**Queue Management**:

- PipelineRuns exceeding concurrency limit are queued
- State: `queued` → `started` → `completed`
- FIFO queue per repository
- Automatic promotion when slots become available

See [Concurrency Flow]({{< relref "flows-diagram" >}}) for details.
**Package**: `pkg/queue`

## Provider Integrations

PAC supports multiple Git providers through a common interface:

### Provider Interface

All providers implement the `Interface` in `pkg/provider/interface.go`:

```go
type Interface interface {
    // Validate webhook signature
    Validate(req *http.Request) error

    // Parse webhook payload
    ParsePayload(payload []byte) (*info.Event, error)

    // Create status check/comment
    CreateStatus(repo string, sha string, status string) error

    // Post comment on PR/MR
    CreateComment(repo string, prNum int, comment string) error

    // Get changed files
    GetFiles(repo string, prNum int) ([]string, error)
}
```

### Supported Providers

- GitHub
- GitLab
- Forgejo
- Bitbucket

**Package**: `pkg/provider/github`**Features**:

- GitHub App authentication
- Webhook authentication
- GitHub Checks API integration
- Line annotations for errors
- Re-run from UI support

**Authentication**:

- GitHub App (recommended)
- Personal Access Token

**Package**: `pkg/provider/gitlab`**Features**:

- Merge Request notes
- Pipeline status badges
- Commit status integration
- Group-level tokens

**Authentication**:

- Personal Access Token
- Project Access Token

**Package**: `pkg/provider/gitea` (shared with Gitea)**Features**:

- Pull request comments
- Commit status
- Empty webhook secrets (optional)

**Status**: Tech Preview**Authentication**:

- Personal Access Token

**Package**: `pkg/provider/bitbucketcloud`, `pkg/provider/bitbucketserver`**Features**:

- Pull request comments
- Build status API
- Separate Cloud/Server implementations

**Authentication**:

- App Password (Cloud)
- Personal Access Token (Server)

## Package Structure

The following is an overview of the main packages:

```text
pkg/
├── acl/              # Access control and permissions
├── adapter/          # Event adapter and webhook processing
├── apis/             # CR definitions and types
├── cel/              # CEL expression evaluation
├── changedfiles/     # File change detection
├── cli/              # tkn-pac CLI implementation
├── formatting/       # Status and output formatting
├── hub/              # Tekton Hub and Artifact Hub integration
├── kubeinteraction/  # Kubernetes API client
├── matcher/          # Event and pipeline matching
├── opscomments/      # GitOps command parsing (/test, /retest)
├── params/           # Configuration and settings
├── pipelineascode/   # Core pipeline processing
├── provider/         # Git provider integrations
├── queue/            # Concurrency queue management
├── reconciler/       # PipelineRun reconciliation
└── secrets/          # Secret management
```

## Configuration and Settings

PAC is configured through:

### ConfigMap Settings

**Name**: `pipelines-as-code` (in `pipelines-as-code` namespace)
**Common Settings**:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
data:
  # Application settings
  application-name: "Pipelines as Code"

  # Artifact Hub API URL
  hub-url: "https://artifacthub.io/api/v1"

  # Remote tasks support
  remote-tasks: "true"

  # Auto-create secrets
  secret-auto-create: "true"

  # Bitbucket settings
  bitbucket-cloud-check-source-ip: "true"

  # Maximum PipelineRuns to keep
  max-keep-runs: "10"
```

### Repository-Level Settings

Settings can be overridden per repository:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  annotations:
    pipelinesascode.tekton.dev/max-keep-runs: "5"
```

## Secrets Management

PAC uses Kubernetes secrets for:

### Git Provider Credentials

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: github-token
type: Opaque
stringData:
  token: "ghp_xxxxx"  # Personal access token or GitHub App token
```

### Webhook Secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: webhook-secret
type: Opaque
stringData:
  webhook.secret: "random-secret-string"
```

### Pipeline Secrets

Secrets for use within pipelines:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: registry-credentials
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64-encoded-config>
```

## Logging and Observability

### Structured Logging

PAC uses structured JSON logging:

```json
{
  "level": "info",
  "ts": "2024-03-01T10:30:00.000Z",
  "logger": "pipelinesascode",
  "msg": "Processing pull request event",
  "provider": "github",
  "repository": "org/repo",
  "event": "pull_request"
}
```

### Log Levels

Configure via ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: config-logging
  namespace: pipelines-as-code
data:
  zap-logger-config: |
    {
      "level": "info",
      "development": false,
      "outputPaths": ["stdout"],
      "errorOutputPaths": ["stderr"],
      "encoding": "json"
    }
```

## Performance Considerations

### Resource Requirements

**Controller**:

- CPU: 100m - 500m
- Memory: 128Mi - 512Mi

**Watcher**:

- CPU: 50m - 200m
- Memory: 64Mi - 256Mi

### Scaling

- Controllers run in active/passive mode (leader election)
- Watchers can run multiple replicas
- Use horizontal pod autoscaling for high-traffic scenarios

### Optimization Tips

- Use concurrency limits to prevent cluster overload
- Configure PipelineRun retention (`max-keep-runs`)
- Enable remote task caching
- Use volume workspaces instead of PVCs for better performance

## Security Architecture

### Webhook Signature Validation

All webhooks are cryptographically verified:

- **GitHub**: HMAC-SHA256 signature
- **GitLab**: Secret token validation
- **Bitbucket**: HMAC-SHA256 signature
- **Forgejo**: Optional signature validation

### Secret Redaction

Secrets are automatically redacted from:

- GitHub Checks annotations
- Log outputs in comments
- PipelineRun status messages

### RBAC

PAC requires specific Kubernetes permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pipelines-as-code-controller
rules:
- apiGroups: ["pipelinesascode.tekton.dev"]
  resources: ["repositories"]
  verbs: ["get", "list", "watch", "update"]
- apiGroups: ["tekton.dev"]
  resources: ["pipelineruns", "taskruns"]
  verbs: ["get", "list", "watch", "create", "delete"]
```

## Extension Points

### Custom Parameters

Inject custom parameters into PipelineRuns:
**Package**: `pkg/customparams`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
data:
  custom-console-name: "Tekton Dashboard"
  custom-console-url: "https://dashboard.example.com"
```

### LLM Integration

PAC includes experimental LLM integration for:

- Automatic issue analysis
- Error explanation
- Suggested fixes

**Package**: `pkg/llm`

## Next Steps

{{< cards >}}
  {{< card link="../flows-diagram" title="Event Flows" subtitle="See detailed event flow diagrams" >}}
  {{< card link="../testing" title="Testing Guide" subtitle="Learn how to test PAC components" >}}
  {{< card link="../setup" title="Development Setup" subtitle="Set up your development environment" >}}
  {{< card link="../" title="Contributing" subtitle="Start contributing to PAC" >}}
{{< /cards >}}
