package tracing

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// RecordingExporter collects exported spans for test assertions.
type RecordingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *RecordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *RecordingExporter) Shutdown(_ context.Context) error { return nil }

func (e *RecordingExporter) GetSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan{}, e.spans...)
}

// SetupTracer installs a test TracerProvider that records spans into the
// returned exporter. The global provider is reset to noop on test cleanup.
func SetupTracer(t *testing.T) *RecordingExporter {
	t.Helper()
	exporter := &RecordingExporter{}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	return exporter
}

func SpanAttr(s sdktrace.ReadOnlySpan, key string) string {
	for _, attr := range s.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.Emit()
		}
	}
	return ""
}

func HasAttr(s sdktrace.ReadOnlySpan, key string) bool {
	for _, attr := range s.Attributes() {
		if string(attr.Key) == key {
			return true
		}
	}
	return false
}

func FindSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}
