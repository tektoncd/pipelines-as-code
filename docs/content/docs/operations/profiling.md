---
title: Profiling
weight: 8
BookToC: true
---

# Profiling PAC Components

Pipelines-as-Code components embed the [Knative profiling server](https://pkg.go.dev/knative.dev/pkg/profiling),
which exposes Go runtime profiling data via the standard `net/http/pprof` endpoints.
Profiling is useful for diagnosing CPU hot-spots, memory growth, goroutine leaks, and
other performance issues.

## How It Works

Each PAC component starts an HTTP server on port **8008** (the default Knative profiling
port, overridable with the `PROFILING_PORT` environment variable). When profiling is
enabled the following endpoints are active:

| Endpoint | Description |
| --- | --- |
| `/debug/pprof/` | Index of all available profiles |
| `/debug/pprof/heap` | Heap memory allocations |
| `/debug/pprof/goroutine` | All current goroutines |
| `/debug/pprof/profile` | 30-second CPU profile |
| `/debug/pprof/trace` | Execution trace |
| `/debug/pprof/cmdline` | Process command line |
| `/debug/pprof/symbol` | Symbol lookup |

When profiling is disabled the server still listens but returns `404` for every request.

## Enabling Profiling

### Watcher

The **watcher** (`pipelines-as-code-watcher`) uses Knative's `sharedmain` framework,
which watches the `config-observability` ConfigMap and toggles profiling **without a
restart**.

**`PAC_DISABLE_HEALTH_PROBE=true` must be set on the watcher, otherwise a port conflict
on 8080 will cause the profiling server to shut down:**

```bash
kubectl set env deployment/pipelines-as-code-watcher \
  -n pipelines-as-code \
  PAC_DISABLE_HEALTH_PROBE=true
```

Then enable profiling via the ConfigMap:

```bash
kubectl patch configmap pipelines-as-code-config-observability \
  -n pipelines-as-code \
  --type merge \
  -p '{"data":{"profiling.enable":"true"}}'
```

To disable profiling:

```bash
kubectl patch configmap pipelines-as-code-config-observability \
  -n pipelines-as-code \
  --type merge \
  -p '{"data":{"profiling.enable":"false"}}'
```

The watcher picks up the ConfigMap change immediately without a restart.

### Webhook

The **webhook** (`pipelines-as-code-webhook`) also uses `sharedmain` and supports
dynamic toggling via the same ConfigMap. Unlike the watcher, the webhook does not run
its own health probe server, so `PAC_DISABLE_HEALTH_PROBE` is not required.

The webhook deployment does not set `CONFIG_OBSERVABILITY_NAME` by default, so it
falls back to looking for a ConfigMap named `config-observability`, which does not
exist in the PAC namespace. Set the environment variable first:

```bash
kubectl set env deployment/pipelines-as-code-webhook \
  -n pipelines-as-code \
  CONFIG_OBSERVABILITY_NAME=pipelines-as-code-config-observability
```

Then use the same `kubectl patch` on the ConfigMap above to enable or disable profiling.

### Controller

The **controller** (`pipelines-as-code-controller`) uses the Knative eventing adapter
framework. Profiling is configured at startup from the `K_METRICS_CONFIG` environment
variable and is **not** dynamically reloaded; a pod restart is required after any change.

The `K_METRICS_CONFIG` variable contains a JSON object whose `ConfigMap` field holds
inline key/value configuration data. To enable profiling, add `"profiling.enable":"true"`
inside that `ConfigMap` object:

```bash
# Read the current value first
kubectl get deployment pipelines-as-code-controller \
  -n pipelines-as-code \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="K_METRICS_CONFIG")].value}'
```

Then patch the Deployment with `profiling.enable` added to the `ConfigMap` field, for example:

```bash
kubectl set env deployment/pipelines-as-code-controller \
  -n pipelines-as-code \
  'K_METRICS_CONFIG={"Domain":"pipelinesascode.tekton.dev/controller","Component":"pac_controller","PrometheusPort":9090,"ConfigMap":{"name":"pipelines-as-code-config-observability","profiling.enable":"true"}}'
```

This triggers a rolling restart of the controller pod. Remove `"profiling.enable":"true"`
(or set it to `"false"`) and re-apply to disable.

## Accessing Profiles

Port 8008 is not declared in the container spec by default. To make it reachable, patch
the target Deployment(s) to add the port:

```bash
for deploy in pipelines-as-code-watcher pipelines-as-code-controller pipelines-as-code-webhook; do
  kubectl patch deployment "$deploy" \
    -n pipelines-as-code \
    --type json \
    -p '[{"op":"add","path":"/spec/template/spec/containers/0/ports/-","value":{"name":"profiling","containerPort":8008,"protocol":"TCP"}}]'
done
```

This triggers a rolling restart of the pod. Once the pod is running, you can access
the pprof endpoints.

### Using `kubectl port-forward`

The recommended way to access the profiling server is with `kubectl port-forward`. This
forwards a local port on your machine to the port on the pod, without exposing it to the
cluster network.

First, get the name of the pod you want to profile. Choose the label that matches the
component:

```bash
# Watcher
export POD_NAME=$(kubectl get pods -n pipelines-as-code \
  -l app.kubernetes.io/name=watcher \
  -o jsonpath='{.items[0].metadata.name}')

# Controller
export POD_NAME=$(kubectl get pods -n pipelines-as-code \
  -l app.kubernetes.io/name=controller \
  -o jsonpath='{.items[0].metadata.name}')

# Webhook
export POD_NAME=$(kubectl get pods -n pipelines-as-code \
  -l app.kubernetes.io/name=webhook \
  -o jsonpath='{.items[0].metadata.name}')
```

Then, forward a local port to the pod's profiling port:

```bash
kubectl port-forward -n pipelines-as-code $POD_NAME 8008:8008
```

The pprof index is now available at `http://localhost:8008/debug/pprof/`.

### Changing the profiling port

If port 8008 conflicts with another service, set the `PROFILING_PORT` environment
variable on the Deployment to use a different port:

```bash
kubectl set env deployment/pipelines-as-code-watcher \
  -n pipelines-as-code \
  PROFILING_PORT=8090
```

Update the `containerPort` in the patch above and your port-forward command to match.

### Capturing profiles with `go tool pprof`

With `kubectl port-forward` running, use `go tool pprof` to analyze profiles directly:

```bash
# Heap profile
go tool pprof http://localhost:8008/debug/pprof/heap

# 30-second CPU profile
go tool pprof http://localhost:8008/debug/pprof/profile

# Goroutine dump
go tool pprof http://localhost:8008/debug/pprof/goroutine
```

### Saving profiles to disk

You can also save profiles to disk for later analysis using `curl`:

```bash
# Save a heap profile
curl -o heap-$(date +%Y%m%d-%H%M%S).pb.gz \
  http://localhost:8008/debug/pprof/heap

# Analyze later - CLI
go tool pprof heap-<timestamp>.pb.gz

# Analyze later - interactive web UI (opens browser at http://localhost:8009)
go tool pprof -http=:8009 heap-<timestamp>.pb.gz
```

## Security Considerations

The profiling server exposes internal runtime data. Because port 8008 is not declared
in the container spec by default, access requires an explicit Deployment patch, limiting
it to users with `deployments/patch` permission in the `pipelines-as-code` namespace.

Do not expose port 8008 via a Service or Ingress in production environments. Disable
profiling (`profiling.enable: "false"`) when not actively investigating an issue.
