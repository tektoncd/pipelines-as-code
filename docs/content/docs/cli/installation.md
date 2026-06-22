---
title: "Installation"
weight: 1
---

This page explains how to install the `tkn pac` CLI. You need this tool to bootstrap, configure, and interact with Pipelines-as-Code from the command line.

## Install

{{< tabs >}}
{{< tab name="Binary" >}}
Download the latest binary directly for your operating system from the
[releases](https://github.com/tektoncd/pipelines-as-code/releases)
page.

Available operating systems are:

* MacOS - M1 and x86 architecture
* Linux - 64bits - RPM, Debian packages, and tarballs.
* Linux - ARM 64bits - RPM, Debian packages, and tarballs.
* Windows - Arm 64 Bits and x86 architecture.

{{< callout type="info" >}}
On Windows, tkn-pac will look for the Kubernetes config in `%USERPROFILE%\.kube\config`. On Linux and MacOS, it will use the standard $HOME/.kube/config.
{{< /callout >}}

{{< /tab >}}

{{< tab name="Homebrew" >}}
The `tkn pac` plug-in is available from Homebrew as a cask.

Before installing, trust the tap (required for Homebrew 5.2+, mandatory in 6.0):

```shell
brew tap openshift-pipelines/pipelines-as-code
brew trust openshift-pipelines/pipelines-as-code
```

Then install with:

```shell
brew install --cask openshift-pipelines/pipelines-as-code/tektoncd-pac
```

To upgrade:

```shell
brew upgrade --cask openshift-pipelines/pipelines-as-code/tektoncd-pac
```

If this is the first time you are adding `tkn-pac` on macOS (not needed on Linux), Gatekeeper may block the binary on first run. Remove the quarantine attribute with:

```shell
xattr -d com.apple.quarantine "$(brew --prefix)/bin/tkn-pac"
```

The `tkn pac` plug-in is compatible with [Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux).

{{< /tab >}}
{{< tab name="Container" >}}
`tkn-pac` is available as a container image:

```shell
# use docker
podman run -e KUBECONFIG=/tmp/kube/config -v ${HOME}/.kube:/tmp/kube \
     -it  ghcr.io/tektoncd/pipelines-as-code/tkn-pac:stable tkn-pac help
```

{{< /tab >}}

{{< tab name="GO" >}}
To install from the Git repository:

```shell
go install github.com/tektoncd/pipelines-as-code/cmd/tkn-pac
```

{{< /tab >}}

{{< tab name="Arch" >}}
You can install the `tkn pac` plugin from the [Arch User
Repository](https://aur.archlinux.org/packages/tkn-pac/) (AUR) with your
favorite AUR installer like `yay`:

```shell
yay -S tkn-pac
```

{{< /tab >}}

{{< /tabs >}}
