---
title: Distributed Tracing
weight: 5
---

This page describes how to enable OpenTelemetry distributed tracing for Pipelines-as-Code. When enabled, PaC emits trace spans for webhook event processing and PipelineRun lifecycle timing.

## Enabling tracing

The ConfigMap `pipelines-as-code-config-observability` controls tracing configuration. It must exist in the same namespace as the Pipelines-as-Code controller and watcher deployments. See [config/305-config-observability.yaml](https://github.com/tektoncd/pipelines-as-code/blob/main/config/305-config-observability.yaml) for the full example.

It contains the following tracing fields:

* `tracing-protocol`: Export protocol. Supported values: `grpc`, `http/protobuf`, `none`. Default is `none` (tracing disabled).
* `tracing-endpoint`: OTLP collector endpoint. Required when protocol is not `none`. The `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable takes precedence if set.
* `tracing-sampling-rate`: Fraction of traces to sample. `0.0` = none, `1.0` = all. Default is `0`.

### Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code-config-observability
  namespace: pipelines-as-code
data:
  tracing-protocol: grpc
  tracing-endpoint: "http://otel-collector.observability.svc.cluster.local:4317"
  tracing-sampling-rate: "1.0"
```

Changes to `tracing-protocol`, `tracing-endpoint`, and `tracing-sampling-rate` require restarting the controller and watcher pods. The trace exporter is created once at startup from the ConfigMap values at that time. Set `tracing-protocol` to `none` or remove the tracing keys to disable tracing.

The controller and watcher locate this ConfigMap by name via the `CONFIG_OBSERVABILITY_NAME` environment variable set in their deployment manifests. Operator-based installations may manage this differently; consult the operator documentation for details.

## Emitted spans

The controller emits a `PipelinesAsCode:ProcessEvent` span for each webhook event. The watcher emits `waitDuration` and `executeDuration` spans for completed PipelineRuns.

### Webhook event span (`PipelinesAsCode:ProcessEvent`)

[OTel VCS semantic conventions](https://opentelemetry.io/docs/specs/semconv/attributes-registry/vcs/):

| Attribute | Source |
| --- | --- |
| `vcs.provider.name` | Git provider name |
| `vcs.repository.url.full` | Repository URL |
| `vcs.ref.head.revision` | Head commit SHA |

PaC-specific:

| Attribute | Source |
| --- | --- |
| `pipelinesascode.tekton.dev.event_type` | Webhook event type |

### PipelineRun timing spans (`waitDuration`, `executeDuration`)

Tekton-compatible bare keys (match Tekton's own reconciler spans for correlation):

| Attribute | Source |
| --- | --- |
| `namespace` | PipelineRun namespace |
| `pipelinerun` | PipelineRun name |

Cross-service delivery attributes (`delivery.tekton.dev.*`):

| Attribute | Source |
| --- | --- |
| `delivery.tekton.dev.pipelinerun_uid` | PipelineRun UID |
| `delivery.tekton.dev.result_message` | First failing TaskRun message; omitted on success; truncated to 1024 bytes |

Additional `delivery.tekton.dev.*` attributes are sourced from [configurable PipelineRun labels](#configuring-label-sourced-attributes).

[OTel CI/CD semantic conventions](https://opentelemetry.io/docs/specs/semconv/attributes-registry/cicd/) (`executeDuration` only):

| Attribute | Source |
| --- | --- |
| `cicd.pipeline.result` | Outcome enum (see below) |

### `cicd.pipeline.result` enum

| Condition | Value |
| --- | --- |
| `Status=True` | `success` |
| `Status=False`, reason `Failed` | `failure` |
| `Status=False`, reason `PipelineRunTimeout` | `timeout` |
| `Status=False`, reason `Cancelled` or `CancelledRunningFinally` | `cancellation` |
| `Status=False`, any other reason | `error` |

## Configuring label-sourced attributes

Some span attributes are read from PipelineRun labels. The label names are configurable via the main `pipelines-as-code` ConfigMap so deployments can point at their existing labels without rewriting producers:

| ConfigMap key | PipelineRun label read (default) | Span attribute emitted |
| --- | --- | --- |
| `tracing-label-action` | `delivery.tekton.dev/action` | `cicd.pipeline.action.name` |
| `tracing-label-application` | `delivery.tekton.dev/application` | `delivery.tekton.dev.application` |
| `tracing-label-component` | `delivery.tekton.dev/component` | `delivery.tekton.dev.component` |

Setting a ConfigMap key to the empty string disables emission of that label-sourced attribute. Only label-sourced attributes are affected; all other span attributes are always emitted. The emitted span attribute keys are fixed regardless of which labels are read, so cross-service queries work uniformly.

Unlike the observability ConfigMap above (which requires a pod restart), changes to these label mappings are picked up automatically without restarting pods.

## Trace context propagation

When Pipelines-as-Code creates a PipelineRun, it sets the `tekton.dev/pipelinerunSpanContext` annotation with a JSON-encoded OTel TextMapCarrier containing the W3C `traceparent`. PaC tracing works independently — you get PaC spans regardless of whether Tekton Pipelines has tracing enabled.

If Tekton Pipelines is also configured with tracing pointing at the same collector, its reconciler spans appear as children of the PaC span, providing a single end-to-end trace from webhook receipt through task execution. See the [Tekton Pipelines tracing documentation](https://github.com/tektoncd/pipeline/blob/main/docs/developers/tracing.md) for Tekton's independent tracing setup.

## Deploying a trace collector

Pipelines-as-Code exports traces using the standard OpenTelemetry Protocol (OTLP). You need a running OTLP-compatible collector for the `tracing-endpoint` to point to. Common options include:

* [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) -- the vendor-neutral reference collector
* [Jaeger](https://www.jaegertracing.io/docs/latest/getting-started/) -- supports OTLP ingestion natively since v1.35

Deploying and operating a collector is outside the scope of Pipelines-as-Code. Refer to your organization's observability infrastructure or the links above for setup instructions.
