---
title: Repository CR
weight: 4
---

This page describes the Repository CR, which configures Pipelines-as-Code to handle events from your Git repositories and defines where PipelineRuns execute.

The Repository CR serves the following purposes:

- Telling Pipelines-as-Code that events from a specific URL need to be handled.
- Specifying the namespace where PipelineRuns execute.
- Referencing an API secret, username, or API URL when required by your Git provider
  (for example, when using webhooks instead of the GitHub App).
- Letting you declare [custom parameters]({{< relref "/docs/advanced/custom-parameters" >}})
  within the PipelineRun that Pipelines-as-Code expands based on certain filters.

To configure Pipelines-as-Code, create a Repository CR in the
namespace where your CI runs -- for example, `project-repository`.

{{< callout type="warning" >}}
You cannot create a Repository CR in the same namespace where
Pipelines-as-Code is deployed (for example
the `openshift-pipelines` or `pipelines-as-code` namespace).
{{< /callout >}}

You can create the Repository CR using the `tkn pac` [CLI]({{< relref
"/docs/cli/" >}}) with `tkn pac create repository`, or by
applying a YAML file with kubectl:

```bash
cat <<EOF | kubectl create -n project-repository -f-
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: project-repository
spec:
  url: "https://github.com/linda/project"
EOF
```

With this configuration, when an event from the `linda/project` repository
occurs, Pipelines-as-Code handles the event and checks
out the contents of `linda/project` to match PipelineRuns in the `.tekton/`
directory.

If a PipelineRun matches the event through its annotations -- for example, on a
specific branch and event like a `push` or `pull_request` -- Pipelines-as-Code starts the
PipelineRun in the namespace where the Repository CR was created. You can only start a
PipelineRun in the namespace where the Repository CR is located.

{{< callout type="info" >}}
Pipelines-as-Code uses a Kubernetes Mutating Admission Webhook to enforce a
single Repository CR per URL in the cluster and to ensure that URLs are valid
and non-empty.

Disabling this webhook is not supported and may pose a security risk in
clusters with untrusted users, as it could allow one user to hijack another's
private repository and gain unauthorized control over it.

If the webhook were disabled, multiple Repository CRs could be created for the
same URL. In this case, only the first created CR would be recognized unless
the user specifies the `target-namespace` annotation in their PipelineRun.
{{< /callout >}}

## Setting PipelineRun definition source

You can add an extra layer of security by using a PipelineRun annotation
to explicitly target a specific namespace. A Repository CR must still
exist in that namespace for Pipelines-as-Code to match it.

This annotation prevents bad actors on a shared cluster from hijacking
PipelineRun execution to a namespace they cannot access. It lets you
tie repository ownership to a specific namespace on your cluster.

To use this feature, add the following annotation to the PipelineRun:

```yaml
pipelinesascode.tekton.dev/target-namespace: "mynamespace"
```

Pipelines-as-Code then matches the repository only in the `mynamespace`
namespace instead of searching all available Repository CRs on the
cluster.

### PipelineRun definition provenance

By default, on a push or a pull request, Pipelines-as-Code fetches the
PipelineRun definition from the branch where the event originated.

You can change this behavior by setting `pipelinerun_provenance`.
This setting currently accepts two values:

- `source`: The default behavior. Pipelines-as-Code fetches the PipelineRun definition
  from the branch where the event originated.
- `default_branch`: Pipelines-as-Code fetches the PipelineRun definition from the default
  branch of the repository as configured on the Git platform (for example,
  `main`, `master`, or `trunk`).

Example:

The following configuration specifies a repository named my-repo with a URL of
<https://github.com/my-org/my-repo>. It sets `pipelinerun_provenance`
to `default_branch`, so Pipelines-as-Code fetches the PipelineRun definition
from the default branch of the repository.

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  settings:
    pipelinerun_provenance: "default_branch"
```

{{< callout type="info" >}}
Setting the provenance of the PipelineRun definition to the default
branch adds another layer of security. It ensures that only users who have the
right to merge commits to the default branch can change the PipelineRun and
access the infrastructure.
{{< /callout >}}
