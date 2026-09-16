---
title: "info"
weight: 10
---

Use `tkn pac info` to display details about your Pipelines-as-Code installation or to test globbing patterns. This command helps you verify your setup and debug glob-based annotations.

## Usage

```shell
tkn pac info [install | globbing] [flags]
```

## Installation Info

By default, the command displays the version of the Pipelines-as-Code controller and the namespace where it is installed. All users on the cluster can access this information through a ConfigMap named `pipelines-as-code-info`, which has broad read access in the Pipelines-as-Code namespace.

If you are a cluster admin, you can also view an overview of all Repository CRs on the cluster, along with their associated URLs.

As an admin, if your installation uses a [GitHub App]({{< relref "/docs/providers/github-app" >}}), you can see the details of the installed application and other relevant information, such as the URL endpoint configured for the GitHub App. By default, this queries the public GitHub API. You can specify a custom GitHub API URL using the `--github-api-url` flag.

## Test Globbing Pattern

Use `tkn pac info globbing` to test whether a glob pattern matches files or strings. This is especially useful when you are configuring annotations such as `on-path-change` or `on-target-branch`.

{{< callout type="warning" >}}
Against a directory, this command matches your local filesystem. Pipelines-as-Code matches the list of changed files your Git provider reports, which is not the same thing. Submodules show the gap: your checkout has their contents, while the provider reports only the submodule directory. A pattern such as `test-submodule/**` matches here and fails at runtime. See [Git submodules]({{< relref "/docs/guides/event-matching/path-matching#git-submodules" >}}).
{{< /callout >}}

### Examples

Match all markdown files in the `docs` directory and its subdirectories:

```bash
tkn pac info globbing 'docs/***/*.md'
```

Test whether the expression `refs/heads/*` matches `refs/heads/main`:

```bash
tkn pac info globbing -s "refs/heads/main" "refs/heads/*"
```

### Flags

* `-d` / `--dir`: Test the glob pattern against a different directory (default: current directory).
* `-s` / `--str`: Test the glob pattern against a string instead of files. Use this for annotations such as `on-target-branch`.

The first argument is the glob pattern to test. If you omit it, the command prompts you for it. Patterns follow the syntax defined by the [glob library](https://github.com/gobwas/glob?tab=readme-ov-file#example).
