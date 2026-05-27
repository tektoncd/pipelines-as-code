package tracing

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
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

func TestShutdownReturnsNilForNoopProvider(t *testing.T) {
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
