package adapter

import (
	"context"
	"net/http"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testtracing "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tracing"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"gotest.tools/v3/assert"
)

func TestSetVCSSpanAttributes(t *testing.T) {
	t.Parallel()

	eventTypeKey := string(tracing.PACEventTypeKey)
	repoURLKey := string(semconv.VCSRepositoryURLFullKey)
	headRevKey := string(semconv.VCSRefHeadRevisionKey)

	tests := []struct {
		name  string
		event *info.Event
		want  map[string]string
	}{
		{
			name: "full event",
			event: &info.Event{
				EventType: "pull_request",
				URL:       "https://github.com/test/repo",
				SHA:       "abc123",
			},
			want: map[string]string{
				eventTypeKey: "pull_request",
				repoURLKey:   "https://github.com/test/repo",
				headRevKey:   "abc123",
			},
		},
		{
			name: "event type only",
			event: &info.Event{
				EventType: "push",
			},
			want: map[string]string{
				eventTypeKey: "push",
			},
		},
		{
			name: "url without sha",
			event: &info.Event{
				EventType: "issue_comment",
				URL:       "https://github.com/test/repo",
			},
			want: map[string]string{
				eventTypeKey: "issue_comment",
				repoURLKey:   "https://github.com/test/repo",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exporter := &testtracing.RecordingExporter{}
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithSyncer(exporter),
			)
			defer func() { _ = tp.Shutdown(context.Background()) }()

			ctx, span := tp.Tracer("test").Start(context.Background(), "test-span")
			setVCSSpanAttributes(ctx, tt.event)
			span.End()

			spans := exporter.GetSpans()
			assert.Equal(t, len(spans), 1)
			got := map[string]string{}
			for _, a := range spans[0].Attributes() {
				got[string(a.Key)] = a.Value.AsString()
			}
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestProcessEventSpanHonorsIncomingTraceContext(t *testing.T) {
	exporter := testtracing.SetupTracer(t)

	// Simulate an external system sending a webhook with a traceparent header.
	// Create a parent span to generate a valid trace context.
	parentCtx, parentSpan := otel.Tracer("external-system").Start(context.Background(), "external-root")
	expectedTraceID := parentSpan.SpanContext().TraceID()
	parentSpan.End()

	// Inject the parent context into HTTP headers (what the webhook sender would do).
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost", nil)
	otel.GetTextMapPropagator().Inject(parentCtx, propagation.HeaderCarrier(req.Header))

	// This is the exact extract → start sequence from handleEvent.
	tracedCtx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(req.Header))
	_, span := otel.Tracer(tracing.TracerName).Start(tracedCtx, "PipelinesAsCode:ProcessEvent",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	span.End()

	spans := exporter.GetSpans()
	var processSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == "PipelinesAsCode:ProcessEvent" {
			processSpan = s
		}
	}
	assert.Assert(t, processSpan != nil, "ProcessEvent span not found")
	assert.Equal(t, processSpan.Parent().TraceID(), expectedTraceID,
		"ProcessEvent span should be parented under the incoming trace context, not a new root")
	assert.Assert(t, processSpan.Parent().IsValid(),
		"ProcessEvent span should have a valid remote parent")
}

func TestProcessEventSpanCreatesRootWithoutIncomingContext(t *testing.T) {
	exporter := testtracing.SetupTracer(t)

	// Webhook with no traceparent header.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost", nil)

	tracedCtx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(req.Header))
	_, span := otel.Tracer(tracing.TracerName).Start(tracedCtx, "PipelinesAsCode:ProcessEvent",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	span.End()

	spans := exporter.GetSpans()
	processSpan := testtracing.FindSpan(spans, "PipelinesAsCode:ProcessEvent")
	assert.Assert(t, processSpan != nil)
	assert.Assert(t, !processSpan.Parent().IsValid(),
		"ProcessEvent span should be a root when no incoming trace context is present")
}
