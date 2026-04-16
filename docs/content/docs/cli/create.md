---
title: "create"
weight: 3
---

Use `tkn pac create` and `tkn pac delete` to manage Repository CRs. These commands let you quickly link a Git repository to Pipelines-as-Code or remove that link when you no longer need it.

## Usage

```shell
tkn pac create repo
tkn pac delete repo <repository-name> [--cascade]
```

## Creating a Repository CR

`tkn pac create repo` creates a new Repository CR linked to your Git repository so that Pipelines-as-Code can execute PipelineRuns in response to Git events. The command also generates a sample [PipelineRun]({{< relref "/docs/guides/creating-pipelines" >}}) in the `.tekton/` directory called `pipelinerun.yaml`, targeting the `main` branch and the `pull_request` and `push` events. You can customize this by editing the [PipelineRun]({{< relref "/docs/guides/creating-pipelines" >}}) to target a different branch or event.

If you have not configured a Git provider previously, the command prompts you to set up a webhook for your provider of choice.

## Deleting a Repository CR

`tkn pac delete repo <repository-name>` deletes a Repository CR. You can also use the full subcommand name, `tkn pac delete repository <repository-name>`.

Specify the `--cascade` flag to also delete the attached secrets (such as webhook or provider secrets) associated with the Repository CR.
