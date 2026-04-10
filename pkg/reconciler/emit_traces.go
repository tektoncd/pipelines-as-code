package reconciler

import (
	"context"
	"encoding/json"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func extractSpanContext(logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) (context.Context, bool) {
	raw, ok := pr.GetAnnotations()[keys.SpanContextAnnotation]
	if !ok || raw == "" {
		return nil, false
	}
	var carrierMap map[string]string
	if err := json.Unmarshal([]byte(raw), &carrierMap); err != nil {
		logger.Warnf("ignoring malformed %s annotation on %s/%s: %v", keys.SpanContextAnnotation, pr.GetNamespace(), pr.GetName(), err)
		return nil, false
	}
	carrier := propagation.MapCarrier(carrierMap)
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil, false
	}
	return ctx, true
}

func emitTimingSpans(logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, cfg *settings.Settings, trStatus map[string]*tektonv1.PipelineRunTaskRunStatus) {
	parentCtx, ok := extractSpanContext(logger, pr)
	if !ok {
		return
	}

	tracer := otel.Tracer(tracing.TracerName)
	commonAttrs := buildCommonAttributes(pr, cfg)

	if pr.Status.StartTime != nil {
		_, waitSpan := tracer.Start(parentCtx, "waitDuration",
			trace.WithTimestamp(pr.CreationTimestamp.Time),
			trace.WithAttributes(commonAttrs...),
		)
		waitSpan.End(trace.WithTimestamp(pr.Status.StartTime.Time))
	}

	if pr.Status.StartTime != nil && pr.Status.CompletionTime != nil {
		extra := buildExecuteAttributes(pr, trStatus)
		execAttrs := make([]attribute.KeyValue, 0, len(commonAttrs)+len(extra))
		execAttrs = append(execAttrs, commonAttrs...)
		execAttrs = append(execAttrs, extra...)
		_, execSpan := tracer.Start(parentCtx, "executeDuration",
			trace.WithTimestamp(pr.Status.StartTime.Time),
			trace.WithAttributes(execAttrs...),
		)
		execSpan.End(trace.WithTimestamp(pr.Status.CompletionTime.Time))
	}
}

func buildCommonAttributes(pr *tektonv1.PipelineRun, cfg *settings.Settings) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 6)
	attrs = append(attrs,
		tracing.NamespaceKey.String(pr.GetNamespace()),
		tracing.PipelineRunKey.String(pr.GetName()),
		tracing.DeliveryPipelineRunUIDKey.String(string(pr.GetUID())),
	)

	if cfg == nil {
		return attrs
	}
	labels := pr.GetLabels()
	labelAttrs := []struct {
		labelName string
		key       attribute.Key
	}{
		{cfg.TracingLabelAction, semconv.CICDPipelineActionNameKey},
		{cfg.TracingLabelApplication, tracing.DeliveryApplicationKey},
		{cfg.TracingLabelComponent, tracing.DeliveryComponentKey},
	}
	for _, m := range labelAttrs {
		if m.labelName == "" {
			continue
		}
		if v := labels[m.labelName]; v != "" {
			attrs = append(attrs, m.key.String(v))
		}
	}
	return attrs
}

func buildExecuteAttributes(pr *tektonv1.PipelineRun, trStatus map[string]*tektonv1.PipelineRunTaskRunStatus) []attribute.KeyValue {
	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	if cond == nil {
		return nil
	}
	attrs := []attribute.KeyValue{tracing.ResultEnum(cond)}
	if cond.Status == corev1.ConditionFalse {
		if msg := failureMessage(cond, trStatus); msg != "" {
			attrs = append(attrs, tracing.DeliveryResultMessageKey.String(tracing.TruncateResultMessage(msg)))
		}
	}
	return attrs
}

// failureMessage returns the most diagnostic error text available for a
// failed PipelineRun. It prefers the earliest failing TaskRun's condition
// message (the err.Error() Tekton wrote via MarkResourceFailed) and falls
// back to the PipelineRun's own condition message for early-stage failures
// that never produced TaskRuns.
func failureMessage(prCond *apis.Condition, trStatus map[string]*tektonv1.PipelineRunTaskRunStatus) string {
	if msg := earliestFailingTaskRunMessage(trStatus); msg != "" {
		return msg
	}
	return prCond.Message
}

func earliestFailingTaskRunMessage(trStatus map[string]*tektonv1.PipelineRunTaskRunStatus) string {
	var (
		earliestTime *metav1.Time
		earliestMsg  string
	)
	for _, ts := range trStatus {
		if ts == nil || ts.Status == nil || ts.Status.CompletionTime == nil {
			continue
		}
		cond := ts.Status.GetCondition(apis.ConditionSucceeded)
		if cond == nil || cond.Status != corev1.ConditionFalse {
			continue
		}
		if earliestTime == nil || ts.Status.CompletionTime.Before(earliestTime) {
			earliestTime = ts.Status.CompletionTime
			earliestMsg = cond.Message
		}
	}
	return earliestMsg
}
