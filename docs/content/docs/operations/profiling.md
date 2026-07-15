---
title: Profiling
weight: 8
BookToC: true
---

# Profiling PAC Components

Pipelines-as-Code components embed the [Knative runtime profiling server](https://pkg.go.dev/knative.dev/pkg/observability/runtime),
which exposes Go runtime profiling data via the standard `net/http/pprof` endpoints.
This is useful for diagnosing CPU hot-spots, memory growth, goroutine leaks, and
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

All components read profiling configuration from the same ConfigMap:

```bash
# Enable
kubectl patch configmap pipelines-as-code-config-observability \
  -n pipelines-as-code \
  --type merge \
  -p '{"data":{"runtime-profiling":"enabled"}}'

# Disable
kubectl patch configmap pipelines-as-code-config-observability \
  -n pipelines-as-code \
  --type merge \
  -p '{"data":{"runtime-profiling":"disabled"}}'
```

### Component-specific prerequisites

The **controller** reads profiling configuration from the `config-observability` ConfigMap
at startup. Unlike the watcher and webhook (which use the `sharedmain` framework), the
controller's eventing adapter does not register a dynamic callback for profiling changes,
so a pod restart is required after enabling or disabling profiling:

```bash
kubectl rollout restart deployment/pipelines-as-code-controller \
  -n pipelines-as-code
```

The **watcher** needs `PAC_DISABLE_HEALTH_PROBE=true`, otherwise a port conflict on
8080 causes the profiling server to shut down. The watcher picks up ConfigMap changes
without a restart.

```bash
kubectl set env deployment/pipelines-as-code-watcher \
  -n pipelines-as-code \
  PAC_DISABLE_HEALTH_PROBE=true
```

The **webhook** needs `CONFIG_OBSERVABILITY_NAME` set explicitly. Without it, the webhook
looks for a ConfigMap called `config-observability`, which does not exist in the PAC
namespace. The webhook picks up ConfigMap changes without a restart.

```bash
kubectl set env deployment/pipelines-as-code-webhook \
  -n pipelines-as-code \
  CONFIG_OBSERVABILITY_NAME=pipelines-as-code-config-observability
```

## Accessing Profiles

The profiling server listens on port **8008** by default. If that conflicts with another
service, set `PROFILING_PORT` on the relevant Deployment(s):

```bash
kubectl set env deployment/pipelines-as-code-watcher \
  deployment/pipelines-as-code-controller \
  deployment/pipelines-as-code-webhook \
  -n pipelines-as-code \
  PROFILING_PORT=8090
```

Port 8008 (or your chosen port) is not declared in the container spec by default, so
you need to patch the target Deployment(s) to expose it:

```bash
PROFILING_PORT="${PROFILING_PORT:-8008}"
for deploy in pipelines-as-code-watcher pipelines-as-code-controller pipelines-as-code-webhook; do
  kubectl patch deployment "$deploy" \
    -n pipelines-as-code \
    --type json \
    -p "[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/ports/-\",\"value\":{\"name\":\"profiling\",\"containerPort\":${PROFILING_PORT},\"protocol\":\"TCP\"}}]"
done
```

This triggers a rolling restart. Once the pod is running you can access the pprof
endpoints.

### Using `kubectl port-forward`

The simplest way to reach the profiling server is `kubectl port-forward`. This forwards
a local port to the pod without exposing it to the cluster network.

First, grab the pod name for the component you want to profile:

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

Then forward the port:

```bash
PROFILING_PORT="${PROFILING_PORT:-8008}"
kubectl port-forward -n pipelines-as-code $POD_NAME ${PROFILING_PORT}:${PROFILING_PORT}
```

The pprof index is now at `http://localhost:${PROFILING_PORT}/debug/pprof/`.

### Capturing profiles with `go tool pprof`

With `kubectl port-forward` running, you can analyze profiles directly:

```bash
# Heap profile
go tool pprof http://localhost:${PROFILING_PORT}/debug/pprof/heap

# 30-second CPU profile
go tool pprof http://localhost:${PROFILING_PORT}/debug/pprof/profile

# Goroutine dump
go tool pprof http://localhost:${PROFILING_PORT}/debug/pprof/goroutine
```

### Saving profiles to disk

You can save profiles for later analysis with `curl`:

```bash
# Save a heap profile
curl -o heap-$(date +%Y%m%d-%H%M%S).pb.gz \
  http://localhost:${PROFILING_PORT}/debug/pprof/heap

# Analyze later
go tool pprof heap-<timestamp>.pb.gz

# Or open the interactive web UI (starts a browser at http://localhost:8009)
go tool pprof -http=:8009 heap-<timestamp>.pb.gz
```

## Security Considerations

The profiling server exposes internal runtime data. Because the profiling port is not
declared in the container spec by default, access requires an explicit Deployment patch,
limiting it to users with `deployments/patch` permission in the `pipelines-as-code`
namespace.

Do not expose the profiling port via a Service or Ingress in production. Disable
profiling (`runtime-profiling: "disabled"`) when you are not actively investigating
an issue.
