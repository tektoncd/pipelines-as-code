package reconciler

import (
	"context"
	"encoding/json"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

const (
	applicationLabel = "appstudio.openshift.io/application"
	componentLabel   = "appstudio.openshift.io/component"
	stageBuild       = "build"
)

// extractSpanContext extracts the trace context from the pipelinerunSpanContext annotation.
func extractSpanContext(pr *tektonv1.PipelineRun) (context.Context, bool) {
	raw, ok := pr.GetAnnotations()[keys.SpanContextAnnotation]
	if !ok || raw == "" {
		return nil, false
	}
	var carrierMap map[string]string
	if err := json.Unmarshal([]byte(raw), &carrierMap); err != nil {
		return nil, false
	}
	carrier := propagation.MapCarrier(carrierMap)
	prop := propagation.TraceContext{}
	ctx := prop.Extract(context.Background(), carrier)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil, false
	}
	return ctx, true
}

// emitTimingSpans emits wait_duration and execute_duration spans for a completed build PipelineRun.
func emitTimingSpans(pr *tektonv1.PipelineRun) {
	parentCtx, ok := extractSpanContext(pr)
	if !ok {
		return
	}

	tracer := otel.GetTracerProvider().Tracer(tracing.TracerName)
	commonAttrs := buildCommonAttributes(pr)

	// Emit wait_duration: creationTimestamp -> status.startTime
	if pr.Status.StartTime != nil {
		_, waitSpan := tracer.Start(parentCtx, "wait_duration",
			trace.WithTimestamp(pr.CreationTimestamp.Time),
			trace.WithAttributes(commonAttrs...),
		)
		waitSpan.End(trace.WithTimestamp(pr.Status.StartTime.Time))
	}

	// Emit execute_duration: status.startTime -> status.completionTime
	if pr.Status.StartTime != nil && pr.Status.CompletionTime != nil {
		execAttrs := append(commonAttrs, buildExecuteAttributes(pr)...)
		_, execSpan := tracer.Start(parentCtx, "execute_duration",
			trace.WithTimestamp(pr.Status.StartTime.Time),
			trace.WithAttributes(execAttrs...),
		)
		execSpan.End(trace.WithTimestamp(pr.Status.CompletionTime.Time))
	}
}

// buildCommonAttributes returns span attributes common to both timing spans.
func buildCommonAttributes(pr *tektonv1.PipelineRun) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("konflux.namespace", pr.GetNamespace()),
		attribute.String("konflux.pipelinerun.name", pr.GetName()),
		attribute.String("konflux.pipelinerun.uid", string(pr.GetUID())),
		attribute.String("konflux.stage", stageBuild),
		attribute.String("konflux.application", pr.GetLabels()[applicationLabel]),
	}
	if component := pr.GetLabels()[componentLabel]; component != "" {
		attrs = append(attrs, attribute.String("konflux.component", component))
	}
	return attrs
}

// buildExecuteAttributes returns span attributes specific to execute_duration.
func buildExecuteAttributes(pr *tektonv1.PipelineRun) []attribute.KeyValue {
	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	success := true
	reason := ""
	if cond != nil {
		reason = cond.Reason
		if cond.Status == corev1.ConditionFalse {
			success = false
		}
	}
	return []attribute.KeyValue{
		attribute.Bool("konflux.success", success),
		attribute.String("konflux.reason", reason),
	}
}
