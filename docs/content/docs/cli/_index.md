---
title: "CLI Reference"
weight: 5
sidebar:
  open: true
---

This section covers the `tkn pac` command-line tool, which you use to install, configure, and manage Pipelines-as-Code resources directly from your terminal.

Pipelines-as-Code provides a CLI designed to work as a plug-in to the [Tekton CLI (tkn)](https://github.com/tektoncd/cli). With `tkn pac`, you can:

* `bootstrap`: Install and configure Pipelines-as-Code with a GitHub App.
* `cel`: Evaluate CEL expressions locally against webhook payloads.
* `completion`: Generate shell completion scripts.
* `create` / `delete`: Create or remove a Repository CR linked to your Git repository.
* `generate`: Scaffold a starter PipelineRun in your `.tekton/` directory.
* `list`: List Repository CRs and their current PipelineRun status.
* `describe`: View details of a Repository CR and its associated runs.
* `logs`: Stream the logs of a PipelineRun attached to a Repository CR.
* `resolve`: Process a PipelineRun locally as Pipelines-as-Code would on the server.
* `info`: Display installation details and test globbing patterns.
* `version`: Show the installed CLI version.
* `webhook`: Add or update webhook secrets for your Git provider.

{{< cards >}}
  {{< card link="installation" title="Installation" subtitle="Install the tkn-pac plugin" >}}
  {{< card link="bootstrap" title="bootstrap" subtitle="Install PAC and create a GitHub App" >}}
  {{< card link="cel" title="cel" subtitle="Evaluate CEL expressions locally" >}}
  {{< card link="completion" title="completion" subtitle="Generate shell completion scripts" >}}
  {{< card link="create" title="create / delete" subtitle="Create or remove a Repository CR" >}}
  {{< card link="repository" title="list" subtitle="List Repository CRs and status" >}}
  {{< card link="describe" title="describe" subtitle="Details of a Repository and its runs" >}}
  {{< card link="generate" title="generate" subtitle="Scaffold a PipelineRun" >}}
  {{< card link="logs" title="logs" subtitle="Stream PipelineRun logs" >}}
  {{< card link="resolve" title="resolve" subtitle="Resolve a PipelineRun locally" >}}
  {{< card link="info" title="info" subtitle="Installation details and globbing" >}}
  {{< card link="version" title="version" subtitle="Show the installed CLI version" >}}
  {{< card link="webhook" title="webhook" subtitle="Add or update webhook secrets" >}}
{{< /cards >}}

## Screenshot

![tkn-plug-in](/images/tkn-pac-cli.png)
