package reconciler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testtracing "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tracing"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

const (
	testActionLabel      = tracing.AttrNamespace + "/action"
	testApplicationLabel = tracing.AttrNamespace + "/application"
	testComponentLabel   = tracing.AttrNamespace + "/component"
)

func testSettings() *settings.Settings {
	return &settings.Settings{
		TracingLabelAction:      testActionLabel,
		TracingLabelApplication: testApplicationLabel,
		TracingLabelComponent:   testComponentLabel,
	}
}

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

func TestEmitTimingSpans(t *testing.T) {
	creationTime := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	startTime := metav1.NewTime(creationTime.Add(30 * time.Second))
	completionTime := metav1.NewTime(creationTime.Add(5 * time.Minute))

	tests := []struct {
		name               string
		annotations        map[string]string
		labels             map[string]string
		uid                types.UID
		namespace          string
		prName             string
		startTime          *metav1.Time
		completionTime     *metav1.Time
		conditionStatus    corev1.ConditionStatus
		conditionReason    string
		conditionMessage   string
		taskFailureMessage string // populated → trStatus contains a failing TaskRun with this message
		skipSpanContext    bool
		wantSpanCount      int
		wantWaitSpan       bool
		wantExecSpan       bool
		wantResult         string
		wantMessage        string // empty → result_message attribute must NOT be present
		wantAction         string
		wantApplication    string
		wantComponent      string
		wantComponentAttr  bool
	}{
		{
			name: "successful PipelineRun emits both spans without result_message",
			labels: map[string]string{
				testActionLabel:      "build",
				testApplicationLabel: "my-app",
				testComponentLabel:   "my-component",
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
			wantResult:        "success",
			wantAction:        "build",
			wantApplication:   "my-app",
			wantComponent:     "my-component",
			wantComponentAttr: true,
		},
		{
			name: "failed PipelineRun carries failing TaskRun message",
			labels: map[string]string{
				testActionLabel:      "build",
				testApplicationLabel: "my-app",
			},
			uid:                "uid-fail",
			namespace:          "ns-fail",
			prName:             "pr-fail",
			startTime:          &startTime,
			completionTime:     &completionTime,
			conditionStatus:    corev1.ConditionFalse,
			conditionReason:    tektonv1.PipelineRunReasonFailed.String(),
			taskFailureMessage: `step "step-build" exited with code 1`,
			wantSpanCount:      2,
			wantWaitSpan:       true,
			wantExecSpan:       true,
			wantResult:         "failure",
			wantMessage:        `step "step-build" exited with code 1`,
			wantAction:         "build",
			wantApplication:    "my-app",
		},
		{
			name: "cancelled PipelineRun maps to cancellation",
			labels: map[string]string{
				testApplicationLabel: "my-app",
			},
			uid:                "uid-cancel",
			namespace:          "ns-cancel",
			prName:             "pr-cancel",
			startTime:          &startTime,
			completionTime:     &completionTime,
			conditionStatus:    corev1.ConditionFalse,
			conditionReason:    tektonv1.PipelineRunReasonCancelled.String(),
			taskFailureMessage: "TaskRun cancelled by user",
			wantSpanCount:      2,
			wantWaitSpan:       true,
			wantExecSpan:       true,
			wantResult:         "cancellation",
			wantMessage:        "TaskRun cancelled by user",
			wantApplication:    "my-app",
		},
		{
			name: "timed-out PipelineRun maps to timeout",
			labels: map[string]string{
				testApplicationLabel: "my-app",
			},
			uid:                "uid-timeout",
			namespace:          "ns-timeout",
			prName:             "pr-timeout",
			startTime:          &startTime,
			completionTime:     &completionTime,
			conditionStatus:    corev1.ConditionFalse,
			conditionReason:    tektonv1.PipelineRunReasonTimedOut.String(),
			taskFailureMessage: "TaskRun \"build\" failed to finish within timeout",
			wantSpanCount:      2,
			wantWaitSpan:       true,
			wantExecSpan:       true,
			wantResult:         "timeout",
			wantMessage:        "TaskRun \"build\" failed to finish within timeout",
			wantApplication:    "my-app",
		},
		{
			name: "validation-error PipelineRun falls back to PR condition message",
			labels: map[string]string{
				testApplicationLabel: "my-app",
			},
			uid:              "uid-error",
			namespace:        "ns-error",
			prName:           "pr-error",
			startTime:        &startTime,
			completionTime:   &completionTime,
			conditionStatus:  corev1.ConditionFalse,
			conditionReason:  tektonv1.PipelineRunReasonFailedValidation.String(),
			conditionMessage: `Pipeline ns-error/pipeline-foo can't be Run; couldn't retrieve referenced task "missing-task"`,
			wantSpanCount:    2,
			wantWaitSpan:     true,
			wantExecSpan:     true,
			wantResult:       "error",
			wantMessage:      `Pipeline ns-error/pipeline-foo can't be Run; couldn't retrieve referenced task "missing-task"`,
			wantApplication:  "my-app",
		},
		{
			name: "completed with skipped tasks maps to success without result_message",
			labels: map[string]string{
				testApplicationLabel: "my-app",
			},
			uid:             "uid-completed",
			namespace:       "ns-completed",
			prName:          "pr-completed",
			startTime:       &startTime,
			completionTime:  &completionTime,
			conditionStatus: corev1.ConditionTrue,
			conditionReason: tektonv1.PipelineRunReasonCompleted.String(),
			wantSpanCount:   2,
			wantWaitSpan:    true,
			wantExecSpan:    true,
			wantResult:      "success",
			wantApplication: "my-app",
		},
		{
			name:            "missing annotation emits no spans",
			annotations:     map[string]string{},
			labels:          map[string]string{},
			skipSpanContext: true,
			wantSpanCount:   0,
		},
		{
			name:           "missing startTime emits no spans",
			labels:         map[string]string{},
			startTime:      nil,
			completionTime: &completionTime,
			wantSpanCount:  0,
		},
		{
			name: "missing completionTime emits only waitDuration",
			labels: map[string]string{
				testApplicationLabel: "my-app",
			},
			uid:             "uid-nocomp",
			namespace:       "ns-nocomp",
			prName:          "pr-nocomp",
			startTime:       &startTime,
			completionTime:  nil,
			wantSpanCount:   1,
			wantWaitSpan:    true,
			wantExecSpan:    false,
			wantApplication: "my-app",
		},
		{
			name:            "no application/component labels emits neither",
			labels:          map[string]string{},
			uid:             "uid-nolabels",
			namespace:       "ns-nolabels",
			prName:          "pr-nolabels",
			startTime:       &startTime,
			completionTime:  &completionTime,
			conditionStatus: corev1.ConditionTrue,
			conditionReason: tektonv1.PipelineRunReasonSuccessful.String(),
			wantSpanCount:   2,
			wantWaitSpan:    true,
			wantExecSpan:    true,
			wantResult:      "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := testtracing.SetupTracer(t)

			annotations := tt.annotations
			if annotations == nil {
				annotations = map[string]string{}
			}
			if !tt.skipSpanContext {
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
						Type:    apis.ConditionSucceeded,
						Status:  tt.conditionStatus,
						Reason:  tt.conditionReason,
						Message: tt.conditionMessage,
					}},
				}
			}

			var trStatus map[string]*tektonv1.PipelineRunTaskRunStatus
			if tt.taskFailureMessage != "" {
				trStatus = map[string]*tektonv1.PipelineRunTaskRunStatus{
					"tr-1": {
						PipelineTaskName: "build",
						Status: &tektonv1.TaskRunStatus{
							Status: duckv1.Status{Conditions: []apis.Condition{{
								Type:    apis.ConditionSucceeded,
								Status:  corev1.ConditionFalse,
								Reason:  tektonv1.TaskRunReasonFailed.String(),
								Message: tt.taskFailureMessage,
							}}},
							TaskRunStatusFields: tektonv1.TaskRunStatusFields{
								CompletionTime: tt.completionTime,
							},
						},
					},
				}
			}

			emitTimingSpans(zap.NewNop().Sugar(), pr, testSettings(), trStatus)

			spans := exporter.GetSpans()
			assert.Equal(t, len(spans), tt.wantSpanCount, "unexpected span count for %s", tt.name)

			if tt.wantWaitSpan {
				ws := testtracing.FindSpan(spans, "waitDuration")
				assert.Assert(t, ws != nil, "expected waitDuration span")
				assert.Equal(t, ws.StartTime(), creationTime)
				assert.Equal(t, ws.EndTime(), tt.startTime.Time)
				assert.Equal(t, testtracing.SpanAttr(ws, string(tracing.NamespaceKey)), tt.namespace)
				assert.Equal(t, testtracing.SpanAttr(ws, string(tracing.PipelineRunKey)), tt.prName)
				assert.Equal(t, testtracing.SpanAttr(ws, string(tracing.DeliveryPipelineRunUIDKey)), string(tt.uid))
				if tt.wantAction != "" {
					assert.Equal(t, testtracing.SpanAttr(ws, string(semconv.CICDPipelineActionNameKey)), tt.wantAction)
				} else {
					assert.Assert(t, !testtracing.HasAttr(ws, string(semconv.CICDPipelineActionNameKey)), "unexpected action attribute")
				}
				if tt.wantApplication != "" {
					assert.Equal(t, testtracing.SpanAttr(ws, string(tracing.DeliveryApplicationKey)), tt.wantApplication)
				} else {
					assert.Assert(t, !testtracing.HasAttr(ws, string(tracing.DeliveryApplicationKey)), "unexpected application attribute")
				}
				if tt.wantComponentAttr {
					assert.Equal(t, testtracing.SpanAttr(ws, string(tracing.DeliveryComponentKey)), tt.wantComponent)
				} else {
					assert.Assert(t, !testtracing.HasAttr(ws, string(tracing.DeliveryComponentKey)), "unexpected component attribute")
				}
			}

			if tt.wantExecSpan {
				es := testtracing.FindSpan(spans, "executeDuration")
				assert.Assert(t, es != nil, "expected executeDuration span")
				assert.Equal(t, es.StartTime(), tt.startTime.Time)
				assert.Equal(t, es.EndTime(), tt.completionTime.Time)
				assert.Equal(t, testtracing.SpanAttr(es, string(tracing.NamespaceKey)), tt.namespace)
				assert.Equal(t, testtracing.SpanAttr(es, string(tracing.PipelineRunKey)), tt.prName)
				assert.Equal(t, testtracing.SpanAttr(es, string(tracing.DeliveryPipelineRunUIDKey)), string(tt.uid))
				assert.Equal(t, testtracing.SpanAttr(es, string(semconv.CICDPipelineResultKey)), tt.wantResult)
				if tt.wantMessage != "" {
					assert.Equal(t, testtracing.SpanAttr(es, string(tracing.DeliveryResultMessageKey)), tt.wantMessage)
				} else {
					assert.Assert(t, !testtracing.HasAttr(es, string(tracing.DeliveryResultMessageKey)), "unexpected result_message attribute")
				}
			}
		})
	}
}

func TestExtractSpanContext(t *testing.T) {
	testtracing.SetupTracer(t)
	validAnnotation, _ := makeSpanContextAnnotation(t)
	ptr := func(s string) *string { return &s }

	tests := []struct {
		name       string
		annotation *string
		wantOK     bool
	}{
		{
			name:       "valid annotation",
			annotation: ptr(validAnnotation),
			wantOK:     true,
		},
		{
			name:   "missing annotation",
			wantOK: false,
		},
		{
			name:       "empty value",
			annotation: ptr(""),
			wantOK:     false,
		},
		{
			name:       "invalid JSON",
			annotation: ptr("not-json"),
			wantOK:     false,
		},
		{
			name:       "valid JSON but invalid traceparent",
			annotation: ptr(`{"traceparent":"invalid"}`),
			wantOK:     false,
		},
		{
			name:       "empty JSON object",
			annotation: ptr(`{}`),
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			annotations := map[string]string{}
			if tt.annotation != nil {
				annotations[keys.SpanContextAnnotation] = *tt.annotation
			}

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
			}

			ctx, ok := extractSpanContext(zap.NewNop().Sugar(), pr)
			assert.Equal(t, ok, tt.wantOK)
			if tt.wantOK {
				assert.Assert(t, ctx != nil)
				sc := trace.SpanContextFromContext(ctx)
				assert.Assert(t, sc.IsValid())
			}
		})
	}
}

func TestExtractSpanContextLogsMalformedJSON(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core).Sugar()

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-pr",
			Annotations: map[string]string{
				keys.SpanContextAnnotation: "not-json",
			},
		},
	}

	_, ok := extractSpanContext(logger, pr)
	assert.Assert(t, !ok)
	assert.Equal(t, logs.Len(), 1)
	assert.Assert(t, logs.All()[0].Level == zapcore.WarnLevel)
	assert.Assert(t, logs.FilterMessageSnippet("malformed").Len() > 0,
		"expected warning about malformed annotation, got: %v", logs.All())
}

func TestEmitTimingSpansTraceParentage(t *testing.T) {
	exporter := testtracing.SetupTracer(t)

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
				testApplicationLabel: "my-app",
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

	emitTimingSpans(zap.NewNop().Sugar(), pr, testSettings(), nil)

	spans := exporter.GetSpans()
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
				testActionLabel:      "build",
				testApplicationLabel: "my-app",
				testComponentLabel:   "my-comp",
			},
		},
	}

	attrs := buildCommonAttributes(pr, testSettings())
	attrMap := make(map[string]attribute.Value)
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value
	}

	assert.Equal(t, attrMap[string(tracing.NamespaceKey)].AsString(), "my-ns")
	assert.Equal(t, attrMap[string(tracing.PipelineRunKey)].AsString(), "my-pr")
	assert.Equal(t, attrMap[string(tracing.DeliveryPipelineRunUIDKey)].AsString(), "my-uid")
	assert.Equal(t, attrMap[string(semconv.CICDPipelineActionNameKey)].AsString(), "build")
	assert.Equal(t, attrMap[string(tracing.DeliveryApplicationKey)].AsString(), "my-app")
	assert.Equal(t, attrMap[string(tracing.DeliveryComponentKey)].AsString(), "my-comp")
}

func TestBuildCommonAttributesNilSettings(t *testing.T) {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pr",
			Namespace: "my-ns",
			UID:       "my-uid",
			Labels: map[string]string{
				testApplicationLabel: "my-app",
			},
		},
	}

	attrs := buildCommonAttributes(pr, nil)
	attrMap := make(map[string]attribute.Value)
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value
	}

	assert.Equal(t, attrMap[string(tracing.NamespaceKey)].AsString(), "my-ns")
	assert.Equal(t, attrMap[string(tracing.PipelineRunKey)].AsString(), "my-pr")
	assert.Equal(t, attrMap[string(tracing.DeliveryPipelineRunUIDKey)].AsString(), "my-uid")
	_, hasApp := attrMap[string(tracing.DeliveryApplicationKey)]
	assert.Assert(t, !hasApp, "no workload attributes when settings is nil")
}

func TestBuildExecuteAttributesSuccess(t *testing.T) {
	pr := &tektonv1.PipelineRun{
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{Conditions: []apis.Condition{{
				Type:   apis.ConditionSucceeded,
				Status: corev1.ConditionTrue,
				Reason: tektonv1.PipelineRunReasonSuccessful.String(),
			}}},
		},
	}

	attrs := buildExecuteAttributes(pr, nil)
	attrMap := make(map[string]attribute.Value)
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value
	}

	assert.Equal(t, attrMap[string(semconv.CICDPipelineResultKey)].AsString(), "success")
	_, hasMsg := attrMap[string(tracing.DeliveryResultMessageKey)]
	assert.Assert(t, !hasMsg, "result_message must not be emitted on success")
}

func TestBuildExecuteAttributesNilCondition(t *testing.T) {
	pr := &tektonv1.PipelineRun{}
	attrs := buildExecuteAttributes(pr, nil)
	assert.Assert(t, attrs == nil, "expected nil attrs when Succeeded condition is absent")
}

func TestEarliestFailingTaskRunMessage(t *testing.T) {
	earlier := metav1.NewTime(time.Date(2025, 3, 1, 10, 1, 0, 0, time.UTC))
	later := metav1.NewTime(time.Date(2025, 3, 1, 10, 5, 0, 0, time.UTC))

	tests := []struct {
		name     string
		trStatus map[string]*tektonv1.PipelineRunTaskRunStatus
		want     string
	}{
		{
			name:     "nil map",
			trStatus: nil,
			want:     "",
		},
		{
			name:     "empty map",
			trStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{},
			want:     "",
		},
		{
			name: "all succeeded",
			trStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"tr-1": newTRStatus(corev1.ConditionTrue, "Succeeded", "All Steps have completed executing", &earlier),
			},
			want: "",
		},
		{
			name: "single failure",
			trStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"tr-1": newTRStatus(corev1.ConditionFalse, "Failed", "step-build exited 1", &earlier),
			},
			want: "step-build exited 1",
		},
		{
			name: "two failures picks earliest by completion time",
			trStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"tr-late":  newTRStatus(corev1.ConditionFalse, "Failed", "downstream failure", &later),
				"tr-early": newTRStatus(corev1.ConditionFalse, "Failed", "root cause failure", &earlier),
			},
			want: "root cause failure",
		},
		{
			name: "mixed statuses ignores succeeded",
			trStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"tr-ok":   newTRStatus(corev1.ConditionTrue, "Succeeded", "All Steps have completed executing", &earlier),
				"tr-fail": newTRStatus(corev1.ConditionFalse, "Failed", "the actual error", &later),
			},
			want: "the actual error",
		},
		{
			name: "skips entries without completion time",
			trStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"tr-incomplete": newTRStatus(corev1.ConditionFalse, "Failed", "still running", nil),
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := earliestFailingTaskRunMessage(tt.trStatus)
			assert.Equal(t, got, tt.want)
		})
	}
}

func newTRStatus(status corev1.ConditionStatus, reason, message string, completion *metav1.Time) *tektonv1.PipelineRunTaskRunStatus {
	return &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "build",
		Status: &tektonv1.TaskRunStatus{
			Status: duckv1.Status{Conditions: []apis.Condition{{
				Type:    apis.ConditionSucceeded,
				Status:  status,
				Reason:  reason,
				Message: message,
			}}},
			TaskRunStatusFields: tektonv1.TaskRunStatusFields{
				CompletionTime: completion,
			},
		},
	}
}
