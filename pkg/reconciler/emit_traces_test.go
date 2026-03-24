package reconciler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

// recordingExporter collects exported spans for test assertions.
type recordingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *recordingExporter) Shutdown(_ context.Context) error { return nil }

func (e *recordingExporter) getSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan{}, e.spans...)
}

// setupTestTracer configures a global tracer provider that records spans.
func setupTestTracer(t *testing.T) *recordingExporter {
	t.Helper()
	exporter := &recordingExporter{}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	})
	return exporter
}

// makeSpanContextAnnotation creates a valid span context annotation value
// and returns it along with the trace ID for assertions.
func makeSpanContextAnnotation(t *testing.T) (string, trace.TraceID) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := tp.Tracer("test").Start(context.Background(), "test-root")
	traceID := span.SpanContext().TraceID()
	span.End()

	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	jsonBytes, err := json.Marshal(map[string]string(carrier))
	assert.NilError(t, err)
	return string(jsonBytes), traceID
}

// spanAttr returns the string value of a named attribute from a span, or "" if not found.
func spanAttr(s sdktrace.ReadOnlySpan, key string) string {
	for _, attr := range s.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.Emit()
		}
	}
	return ""
}

// spanAttrBool returns the bool value of a named attribute from a span.
func spanAttrBool(s sdktrace.ReadOnlySpan, key string) (bool, bool) {
	for _, attr := range s.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsBool(), true
		}
	}
	return false, false
}

// hasAttr returns true if the span has an attribute with the given key.
func hasAttr(s sdktrace.ReadOnlySpan, key string) bool {
	for _, attr := range s.Attributes() {
		if string(attr.Key) == key {
			return true
		}
	}
	return false
}

// findSpan returns the first span with the given name, or nil.
func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func TestEmitTimingSpans(t *testing.T) {
	creationTime := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	startTime := metav1.NewTime(creationTime.Add(30 * time.Second))
	completionTime := metav1.NewTime(creationTime.Add(5 * time.Minute))

	tests := []struct {
		name              string
		annotations       map[string]string
		labels            map[string]string
		uid               types.UID
		namespace         string
		prName            string
		startTime         *metav1.Time
		completionTime    *metav1.Time
		conditionStatus   corev1.ConditionStatus
		conditionReason   string
		wantSpanCount     int
		wantWaitSpan      bool
		wantExecSpan      bool
		wantSuccess       bool
		wantReason        string
		wantApplication   string
		wantComponent     string
		wantComponentAttr bool
	}{
		{
			name:        "successful PipelineRun emits both spans",
			annotations: map[string]string{
				// SpanContextAnnotation will be set in the test body
			},
			labels: map[string]string{
				applicationLabel: "my-app",
				componentLabel:   "my-component",
			},
			uid:               "test-uid-123",
			namespace:         "test-ns",
			prName:            "test-pr",
			startTime:         &startTime,
			completionTime:    &completionTime,
			conditionStatus:   corev1.ConditionTrue,
			conditionReason:   tektonv1.PipelineRunReasonSuccessful.String(),
			wantSpanCount:     2,
			wantWaitSpan:      true,
			wantExecSpan:      true,
			wantSuccess:       true,
			wantReason:        tektonv1.PipelineRunReasonSuccessful.String(),
			wantApplication:   "my-app",
			wantComponent:     "my-component",
			wantComponentAttr: true,
		},
		{
			name:              "failed PipelineRun",
			labels:            map[string]string{applicationLabel: "my-app"},
			uid:               "uid-fail",
			namespace:         "ns-fail",
			prName:            "pr-fail",
			startTime:         &startTime,
			completionTime:    &completionTime,
			conditionStatus:   corev1.ConditionFalse,
			conditionReason:   tektonv1.PipelineRunReasonFailed.String(),
			wantSpanCount:     2,
			wantWaitSpan:      true,
			wantExecSpan:      true,
			wantSuccess:       false,
			wantReason:        tektonv1.PipelineRunReasonFailed.String(),
			wantApplication:   "my-app",
			wantComponentAttr: false,
		},
		{
			name:              "cancelled PipelineRun",
			labels:            map[string]string{applicationLabel: "my-app"},
			uid:               "uid-cancel",
			namespace:         "ns-cancel",
			prName:            "pr-cancel",
			startTime:         &startTime,
			completionTime:    &completionTime,
			conditionStatus:   corev1.ConditionFalse,
			conditionReason:   tektonv1.PipelineRunReasonCancelled.String(),
			wantSpanCount:     2,
			wantWaitSpan:      true,
			wantExecSpan:      true,
			wantSuccess:       false,
			wantReason:        tektonv1.PipelineRunReasonCancelled.String(),
			wantApplication:   "my-app",
			wantComponentAttr: false,
		},
		{
			name:          "missing annotation emits no spans",
			annotations:   map[string]string{},
			labels:        map[string]string{},
			wantSpanCount: 0,
		},
		{
			name:           "missing startTime emits no spans",
			labels:         map[string]string{},
			startTime:      nil,
			completionTime: &completionTime,
			wantSpanCount:  0,
		},
		{
			name:              "missing completionTime emits only waitDuration",
			labels:            map[string]string{applicationLabel: "my-app"},
			uid:               "uid-nocomp",
			namespace:         "ns-nocomp",
			prName:            "pr-nocomp",
			startTime:         &startTime,
			completionTime:    nil,
			wantSpanCount:     1,
			wantWaitSpan:      true,
			wantExecSpan:      false,
			wantApplication:   "my-app",
			wantComponentAttr: false,
		},
		{
			name:              "no application/component labels",
			labels:            map[string]string{},
			uid:               "uid-nolabels",
			namespace:         "ns-nolabels",
			prName:            "pr-nolabels",
			startTime:         &startTime,
			completionTime:    &completionTime,
			conditionStatus:   corev1.ConditionTrue,
			conditionReason:   tektonv1.PipelineRunReasonSuccessful.String(),
			wantSpanCount:     2,
			wantWaitSpan:      true,
			wantExecSpan:      true,
			wantSuccess:       true,
			wantReason:        tektonv1.PipelineRunReasonSuccessful.String(),
			wantApplication:   "",
			wantComponentAttr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := setupTestTracer(t)

			annotations := tt.annotations
			if annotations == nil {
				annotations = map[string]string{}
			}

			// Add span context annotation for cases that should emit spans
			// (except the "missing annotation" test case which has empty annotations)
			if tt.name != "missing annotation emits no spans" {
				annValue, _ := makeSpanContextAnnotation(t)
				annotations[keys.SpanContextAnnotation] = annValue
			}

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:              tt.prName,
					Namespace:         tt.namespace,
					UID:               tt.uid,
					CreationTimestamp: metav1.NewTime(creationTime),
					Annotations:       annotations,
					Labels:            tt.labels,
				},
				Status: tektonv1.PipelineRunStatus{
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						StartTime:      tt.startTime,
						CompletionTime: tt.completionTime,
					},
				},
			}

			if tt.conditionReason != "" {
				pr.Status.Status = duckv1.Status{
					Conditions: []apis.Condition{{
						Type:   apis.ConditionSucceeded,
						Status: tt.conditionStatus,
						Reason: tt.conditionReason,
					}},
				}
			}

			emitTimingSpans(pr)

			spans := exporter.getSpans()
			assert.Equal(t, len(spans), tt.wantSpanCount, "unexpected span count for %s", tt.name)

			if tt.wantWaitSpan {
				ws := findSpan(spans, "waitDuration")
				assert.Assert(t, ws != nil, "expected waitDuration span")
				assert.Equal(t, ws.StartTime(), creationTime)
				assert.Equal(t, ws.EndTime(), tt.startTime.Time)
				assert.Equal(t, spanAttr(ws, "konflux.namespace"), tt.namespace)
				assert.Equal(t, spanAttr(ws, "konflux.pipelinerun.name"), tt.prName)
				assert.Equal(t, spanAttr(ws, "konflux.pipelinerun.uid"), string(tt.uid))
				assert.Equal(t, spanAttr(ws, "konflux.stage"), "build")
				assert.Equal(t, spanAttr(ws, "konflux.application"), tt.wantApplication)
				if tt.wantComponentAttr {
					assert.Equal(t, spanAttr(ws, "konflux.component"), tt.wantComponent)
				} else {
					assert.Assert(t, !hasAttr(ws, "konflux.component"), "unexpected konflux.component attribute")
				}
			}

			if tt.wantExecSpan {
				es := findSpan(spans, "executeDuration")
				assert.Assert(t, es != nil, "expected executeDuration span")
				assert.Equal(t, es.StartTime(), tt.startTime.Time)
				assert.Equal(t, es.EndTime(), tt.completionTime.Time)
				assert.Equal(t, spanAttr(es, "konflux.namespace"), tt.namespace)
				assert.Equal(t, spanAttr(es, "konflux.pipelinerun.name"), tt.prName)
				assert.Equal(t, spanAttr(es, "konflux.pipelinerun.uid"), string(tt.uid))
				assert.Equal(t, spanAttr(es, "konflux.stage"), "build")

				success, found := spanAttrBool(es, "konflux.success")
				assert.Assert(t, found, "expected konflux.success attribute")
				assert.Equal(t, success, tt.wantSuccess)
				assert.Equal(t, spanAttr(es, "konflux.reason"), tt.wantReason)
			}
		})
	}
}

func TestExtractSpanContext(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		wantOK     bool
	}{
		{
			name:       "valid annotation",
			annotation: "VALID", // placeholder, replaced in test
			wantOK:     true,
		},
		{
			name:       "empty annotation",
			annotation: "",
			wantOK:     false,
		},
		{
			name:       "invalid JSON",
			annotation: "not-json",
			wantOK:     false,
		},
		{
			name:       "valid JSON but invalid traceparent",
			annotation: `{"traceparent":"invalid"}`,
			wantOK:     false,
		},
		{
			name:       "empty JSON object",
			annotation: `{}`,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			annotations := map[string]string{}
			if tt.annotation == "VALID" {
				annValue, _ := makeSpanContextAnnotation(t)
				annotations[keys.SpanContextAnnotation] = annValue
			} else if tt.annotation != "" {
				annotations[keys.SpanContextAnnotation] = tt.annotation
			}

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
			}

			ctx, ok := extractSpanContext(pr)
			assert.Equal(t, ok, tt.wantOK)
			if tt.wantOK {
				assert.Assert(t, ctx != nil)
				sc := trace.SpanContextFromContext(ctx)
				assert.Assert(t, sc.IsValid())
			}
		})
	}
}

func TestEmitTimingSpansTraceParentage(t *testing.T) {
	exporter := setupTestTracer(t)

	annValue, traceID := makeSpanContextAnnotation(t)
	startTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	completionTime := metav1.NewTime(time.Now())

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "test-ns",
			UID:               "test-uid",
			CreationTimestamp: metav1.NewTime(startTime.Add(-30 * time.Second)),
			Annotations: map[string]string{
				keys.SpanContextAnnotation: annValue,
			},
			Labels: map[string]string{
				applicationLabel: "my-app",
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{Conditions: []apis.Condition{{
				Type:   apis.ConditionSucceeded,
				Status: corev1.ConditionTrue,
				Reason: tektonv1.PipelineRunReasonSuccessful.String(),
			}}},
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				StartTime:      &startTime,
				CompletionTime: &completionTime,
			},
		},
	}

	emitTimingSpans(pr)

	spans := exporter.getSpans()
	assert.Equal(t, len(spans), 2)

	for _, s := range spans {
		assert.Equal(t, s.Parent().TraceID(), traceID,
			"span %s should have the same trace ID as the parent", s.Name())
		assert.Assert(t, s.Parent().IsValid(),
			"span %s should have a valid parent span context", s.Name())
	}
}

func TestBuildCommonAttributes(t *testing.T) {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pr",
			Namespace: "my-ns",
			UID:       "my-uid",
			Labels: map[string]string{
				applicationLabel: "my-app",
				componentLabel:   "my-comp",
			},
		},
	}

	attrs := buildCommonAttributes(pr)
	attrMap := make(map[string]attribute.Value)
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value
	}

	assert.Equal(t, attrMap["konflux.namespace"].AsString(), "my-ns")
	assert.Equal(t, attrMap["konflux.pipelinerun.name"].AsString(), "my-pr")
	assert.Equal(t, attrMap["konflux.pipelinerun.uid"].AsString(), "my-uid")
	assert.Equal(t, attrMap["konflux.stage"].AsString(), "build")
	assert.Equal(t, attrMap["konflux.application"].AsString(), "my-app")
	assert.Equal(t, attrMap["konflux.component"].AsString(), "my-comp")
}

func TestBuildExecuteAttributes(t *testing.T) {
	tests := []struct {
		name        string
		status      corev1.ConditionStatus
		reason      string
		wantSuccess bool
		wantReason  string
	}{
		{
			name:        "success",
			status:      corev1.ConditionTrue,
			reason:      tektonv1.PipelineRunReasonSuccessful.String(),
			wantSuccess: true,
			wantReason:  tektonv1.PipelineRunReasonSuccessful.String(),
		},
		{
			name:        "failure",
			status:      corev1.ConditionFalse,
			reason:      tektonv1.PipelineRunReasonFailed.String(),
			wantSuccess: false,
			wantReason:  tektonv1.PipelineRunReasonFailed.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &tektonv1.PipelineRun{
				Status: tektonv1.PipelineRunStatus{
					Status: duckv1.Status{Conditions: []apis.Condition{{
						Type:   apis.ConditionSucceeded,
						Status: tt.status,
						Reason: tt.reason,
					}}},
				},
			}

			attrs := buildExecuteAttributes(pr)
			attrMap := make(map[string]attribute.Value)
			for _, a := range attrs {
				attrMap[string(a.Key)] = a.Value
			}

			assert.Equal(t, attrMap["konflux.success"].AsBool(), tt.wantSuccess)
			assert.Equal(t, attrMap["konflux.reason"].AsString(), tt.wantReason)
		})
	}
}
