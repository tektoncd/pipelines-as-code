---
title: Settings
weight: 7
---

Pipelines-as-Code uses two layers of configuration that work together:

| Layer              | Resource                        | Scope                                              |
|--------------------|---------------------------------|----------------------------------------------------|
| **Global**         | `pipelines-as-code` ConfigMap   | Cluster-wide defaults, set by administrators       |
| **Per-repository** | `Repository` CR `spec.settings` | Per-repository overrides, set by repository owners |

## Global configuration

The `pipelines-as-code` ConfigMap in the `pipelines-as-code` namespace controls
cluster-wide behavior: authentication defaults, Hub catalogs, error detection,
retention policies, concurrency, and more.

See the [ConfigMap Reference]({{< relref "/docs/api/configmap" >}}) for all fields
and their default values.

For instructions on viewing and applying changes, see
[Configuration]({{< relref "configuration" >}}).

### Trusted provider hostnames

Pipelines-as-Code only sends Git provider credentials to hostnames it trusts,
listed in the `trusted-provider-hostnames` key:

```bash
kubectl -n pipelines-as-code patch configmap pipelines-as-code \
  --type merge -p '{"data":{"trusted-provider-hostnames":"ghe.example.com"}}'
```

While the key is empty, the public hostnames (`github.com`, `gitlab.com`,
`bitbucket.org`, `gitea.com`, `codeberg.org`) stay trusted and every request the
provider itself authenticated adds its hostname to the
`pipelinesascode.tekton.dev/auto-trusted-provider-hostnames` annotation. A
default installation needs no configuration, and a controller serving several
instances learns all of them.

A non-empty list is authoritative for every hostname, public ones included, and
the controller stops learning hosts. Listing only your own instance is how you
say "this controller must never talk to a public SaaS instance".

That automatic trust only covers requests the provider signed:
[incoming webhooks]({{< relref "/docs/advanced/incoming-webhooks.md" >}}) carry
no provider signature and fail until the hostname is trusted. Set the key
explicitly on any self-hosted instance.

Emptying the list returns to controller-managed mode and makes previously
learned hosts effective again. Delete the auto-trusted annotation as well when
you want the controller to forget them.

Today the allowlist gates the GitHub provider; the other providers are being
moved onto it.

See the [ConfigMap Reference]({{< relref "/docs/api/configmap#param-trusted-provider-hostnames" >}})
for details.

## Per-repository configuration

Each `Repository` CR can include a `spec.settings` block that overrides selected
global defaults for that repository. This covers provider-specific options,
concurrency limits, and pipeline event policies.

See the [Repository CR Settings Reference]({{< relref "/docs/api/settings" >}}) for all fields.

## Configuration inheritance

When both layers define the same setting, the Repository CR value takes
precedence for that repository. Global settings apply wherever no Repository CR
override is present.

See [Global Repository Settings]({{< relref "global-repository-settings" >}}) for
details on the inheritance model and how to configure shared defaults using a
global Repository CR.

## Status information

Pipelines-as-Code exposes read-only status through the `pipelines-as-code-info`
ConfigMap in the `pipelines-as-code` namespace. Any authenticated user can read
this ConfigMap. It contains:

- `version` — the installed version of Pipelines-as-Code
- `controller-url` — the controller URL configured during bootstrap or by the operator
- `provider` — the detected provider type (for example, `GitHub App`)
