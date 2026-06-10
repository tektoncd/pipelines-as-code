---
title: Distributed Tracing
weight: 5
---

This page describes how to enable OpenTelemetry distributed tracing for Pipelines-as-Code. When enabled, PaC emits trace spans for webhook event processing and PipelineRun lifecycle timing.

## Enabling tracing

Pipelines-as-Code reads tracing configuration from standard OpenTelemetry environment variables on the controller and watcher pods:

* `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP collector endpoint URL. Required to enable tracing.
* `OTEL_TRACES_SAMPLER`: Sampler family. Required to enable tracing. Supported values: `always_on`, `always_off`, `traceidratio`, `parentbased_always_on`, `parentbased_always_off`, `parentbased_traceidratio`.
* `OTEL_TRACES_SAMPLER_ARG`: Numeric argument for ratio-based samplers. Set to `0.1` with `OTEL_TRACES_SAMPLER=parentbased_traceidratio` to sample 10% of root traces while keeping the chain coherent on those that are kept.
* `OTEL_EXPORTER_OTLP_PROTOCOL` (or traces-specific `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`): OTLP transport. Supported values: `grpc`, `http/protobuf`. Default: `grpc`.

Both `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_TRACES_SAMPLER` must be set to opt in to tracing. If either is unset PaC falls back to a noop tracer that emits no spans and incurs no exporter cost. Changes to any of these env vars take effect on the next pod restart.

PaC honors inbound `traceparent` headers on incoming webhook requests via the W3C TraceContext propagator. OTel Baggage is intentionally not honored: every emission point in PaC already has the attributes it needs from the local PipelineRun and webhook event, so no cross-service attribute propagation channel is needed.

### Sampler choice and chain coherency

The `parentbased_*` sampler family honors the parent span's sample decision carried in the W3C `traceparent` flag bit. When every service in the delivery chain uses parent-based samplers, the root span's sampling decision propagates end to end: each service either keeps its spans or drops them based on what the root chose. Flat-rate samplers (`traceidratio` without parent-based) cause each service to roll independently, which at fractional sampling fragments the chain into orphaned spans whose `parent_spanID` references a span that was dropped. `parentbased_always_on` keeps everything; `parentbased_traceidratio` with a numeric argument samples a coherent fraction.

## Emitted spans

The controller emits a `PipelinesAsCode:ProcessEvent` span for each webhook event. The watcher emits `waitDuration` and `executeDuration` spans for completed PipelineRuns. The OTel resource attribute `service.name` on all emitted spans is `pipelines-as-code`.

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

Pipelines-as-Code exports traces using the standard OpenTelemetry Protocol (OTLP). You need a running OTLP-compatible collector for `OTEL_EXPORTER_OTLP_ENDPOINT` to point to. Common options include:

* [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) -- the vendor-neutral reference collector
* [Jaeger](https://www.jaegertracing.io/docs/latest/getting-started/) -- supports OTLP ingestion natively since v1.35

Deploying and operating a collector is outside the scope of Pipelines-as-Code. Refer to your organization's observability infrastructure or the links above for setup instructions.
