package tracing

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	knativetracing "knative.dev/pkg/observability/tracing"
)

func nopLogger() *zap.SugaredLogger {
	return zap.NewNop().Sugar()
}

func saveGlobalProvider(t *testing.T) {
	t.Helper()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
}

func TestNewReturnsNoopWhenEndpointUnset(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "")
	t.Setenv(EnvTracesSampler, "always_on")

	tp := New(nopLogger())
	assert.Assert(t, tp != nil)

	_, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	assert.Assert(t, !isSDK, "should not install SDK provider when endpoint unset")
}

func TestNewReturnsNoopWhenSamplerUnset(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "http://localhost:4317")
	t.Setenv(EnvTracesSampler, "")

	tp := New(nopLogger())
	assert.Assert(t, tp != nil)

	_, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	assert.Assert(t, !isSDK, "should not install SDK provider when sampler unset")
}

func TestNewInstallsSDKAndW3CPropagatorOnGRPC(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "http://localhost:4317")
	t.Setenv(EnvTracesSampler, "parentbased_always_on")
	t.Setenv(EnvOTLPProtocol, "grpc")

	tp := New(nopLogger())
	assert.Assert(t, tp != nil)

	_, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	assert.Assert(t, isSDK, "should install SDK provider when endpoint and sampler are both set")

	_, isW3C := otel.GetTextMapPropagator().(propagation.TraceContext)
	assert.Assert(t, isW3C, "should set W3C TraceContext as the global propagator")

	assert.NilError(t, tp.Shutdown(context.Background()))
}

func TestNewInstallsSDKOnHTTPProtobuf(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "http://localhost:4318")
	t.Setenv(EnvTracesSampler, "parentbased_traceidratio")
	t.Setenv(EnvTracesSamplerArg, "0.5")
	t.Setenv(EnvOTLPProtocol, "http/protobuf")

	tp := New(nopLogger())

	_, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	assert.Assert(t, isSDK, "http/protobuf protocol should also install an SDK provider")

	assert.NilError(t, tp.Shutdown(context.Background()))
}

func TestProtocolFromEnv(t *testing.T) {
	tests := []struct {
		name           string
		tracesProtocol string
		otlpProtocol   string
		want           string
	}{
		{"defaults to grpc when neither is set", "", "", protocolGRPC},
		{"falls back to OTLPProtocol when the traces-specific var is unset", "", "http/protobuf", "http/protobuf"},
		{"traces-specific takes precedence over the generic var", "grpc", "http/protobuf", "grpc"},
		{"OTLPProtocol grpc applies", "", "grpc", "grpc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvOTLPTracesProtocol, tt.tracesProtocol)
			t.Setenv(EnvOTLPProtocol, tt.otlpProtocol)
			assert.Equal(t, protocolFromEnv(), tt.want)
		})
	}
}

func TestSamplerFromEnv(t *testing.T) {
	tests := []struct {
		name           string
		sampler        string
		arg            string
		wantDescPrefix string
	}{
		{"always_on", "always_on", "", "AlwaysOnSampler"},
		{"always_off", "always_off", "", "AlwaysOffSampler"},
		{"traceidratio half", "traceidratio", "0.5", "TraceIDRatioBased{0.5}"},
		{"parentbased_always_on", "parentbased_always_on", "", "ParentBased{root:AlwaysOnSampler"},
		{"parentbased_always_off", "parentbased_always_off", "", "ParentBased{root:AlwaysOffSampler"},
		{"parentbased_traceidratio one tenth", "parentbased_traceidratio", "0.1", "ParentBased{root:TraceIDRatioBased{0.1}"},
		{"unrecognized falls back to never sample", "some-future-otel-keyword", "", "AlwaysOffSampler"},
		{"empty value falls back to never sample", "", "", "AlwaysOffSampler"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvTracesSampler, tt.sampler)
			t.Setenv(EnvTracesSamplerArg, tt.arg)
			s := samplerFromEnv(nopLogger())
			assert.Assert(t, strings.Contains(s.Description(), tt.wantDescPrefix),
				"expected sampler description containing %q, got %q", tt.wantDescPrefix, s.Description())
		})
	}
}

func TestShutdownReturnsNilForPassthroughProvider(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "")
	t.Setenv(EnvTracesSampler, "")

	tp := New(nopLogger())
	assert.NilError(t, tp.Shutdown(context.Background()))
}

func TestShutdownIsIdempotent(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "http://localhost:4317")
	t.Setenv(EnvTracesSampler, "always_on")

	tp := New(nopLogger())
	assert.NilError(t, tp.Shutdown(context.Background()))
	assert.NilError(t, tp.Shutdown(context.Background()), "second shutdown must not panic or error")
}

func TestShutdownOnProviderWithoutHookReturnsNil(t *testing.T) {
	tp := &TracerProvider{}
	assert.NilError(t, tp.Shutdown(context.Background()))
}

func TestNewPreservesExistingProviderWhenOTelUnset(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "")
	t.Setenv(EnvTracesSampler, "")

	existing := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(existing)

	_ = New(nopLogger())

	got, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	assert.Assert(t, ok, "global should remain the previously-set SDK provider")
	assert.Equal(t, got, existing, "global should be the same instance as before")
}

func TestGlobalIsNoop(t *testing.T) {
	saveGlobalProvider(t)

	otel.SetTracerProvider(noop.NewTracerProvider())
	assert.Assert(t, globalIsNoop(), "bare noop provider should be detected")

	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	assert.Assert(t, !globalIsNoop(), "bare SDK provider should not be detected as noop")

	knativeNoop, err := knativetracing.NewTracerProvider(context.Background(), knativetracing.Config{Protocol: knativetracing.ProtocolNone})
	assert.NilError(t, err)
	otel.SetTracerProvider(knativeNoop)
	assert.Assert(t, globalIsNoop(), "Knative wrapper around noop should be detected")

	knativeSDK := &knativetracing.TracerProvider{TracerProvider: sdktrace.NewTracerProvider()}
	otel.SetTracerProvider(knativeSDK)
	assert.Assert(t, !globalIsNoop(), "Knative wrapper around SDK provider should not be detected as noop")
}

func TestNewWarnsWhenBothBackendsConfigured(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "http://localhost:4317")
	t.Setenv(EnvTracesSampler, "always_on")

	otel.SetTracerProvider(sdktrace.NewTracerProvider())

	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core).Sugar()

	_ = New(logger)

	matches := observed.FilterMessageSnippet("OpenTelemetry and Knative tracing both configured").All()
	assert.Assert(t, len(matches) == 1, "should emit exactly one dual-config warning")
}

func TestSemconvSchemaURLCompatibleWithResourceDefault(t *testing.T) {
	merged, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("test"),
		),
	)
	assert.NilError(t, err)
	assert.Assert(t, merged != nil)
}

func TestNewDoesNotWarnWhenOnlyOTelConfigured(t *testing.T) {
	saveGlobalProvider(t)
	t.Setenv(EnvOTLPEndpoint, "http://localhost:4317")
	t.Setenv(EnvTracesSampler, "always_on")

	// Default Knative deployment: tracing-protocol "none" returns a noop-wrapped provider.
	knativeNoop, err := knativetracing.NewTracerProvider(context.Background(), knativetracing.Config{Protocol: knativetracing.ProtocolNone})
	assert.NilError(t, err)
	otel.SetTracerProvider(knativeNoop)

	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core).Sugar()

	_ = New(logger)

	matches := observed.FilterMessageSnippet("both configured").All()
	assert.Equal(t, len(matches), 0, "should not warn when only OTel is configured")
}
