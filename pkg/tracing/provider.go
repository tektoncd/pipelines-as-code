package tracing

import (
	"context"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
	knativetracing "knative.dev/pkg/observability/tracing"
)

const (
	EnvOTLPEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvOTLPProtocol       = "OTEL_EXPORTER_OTLP_PROTOCOL"
	EnvOTLPTracesProtocol = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	EnvTracesSampler      = "OTEL_TRACES_SAMPLER"
	EnvTracesSamplerArg   = "OTEL_TRACES_SAMPLER_ARG"

	protocolGRPC = "grpc"
	protocolHTTP = "http/protobuf"
)

type TracerProvider struct {
	shutdown func(context.Context) error
}

func New(logger *zap.SugaredLogger) *TracerProvider {
	otelConfigured := os.Getenv(EnvOTLPEndpoint) != "" && os.Getenv(EnvTracesSampler) != ""
	if otelConfigured && !globalIsNoop() {
		logger.Warn("OpenTelemetry and Knative tracing both configured; spans go through OpenTelemetry, Knative's tracer is unused. Set `tracing-protocol: none` in `pipelines-as-code-config-observability` to disable Knative, or unset `OTEL_EXPORTER_OTLP_ENDPOINT` to disable OpenTelemetry.")
	}

	if os.Getenv(EnvOTLPEndpoint) == "" {
		logger.Info("OpenTelemetry not configured (OTLP endpoint missing)")
		return passthroughProvider()
	}
	if os.Getenv(EnvTracesSampler) == "" {
		logger.Info("OpenTelemetry not configured (sampler missing)")
		return passthroughProvider()
	}

	proto := protocolFromEnv()
	exporter, err := newExporter(context.Background(), logger, proto)
	if err != nil {
		logger.Errorw("failed to create OTLP exporter", "error", err)
		return passthroughProvider()
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(TracerName),
		),
	)
	if err != nil {
		logger.Errorw("failed to create resource", "error", err)
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerFromEnv(logger)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	logger.Infow("tracing initialized", "endpoint", os.Getenv(EnvOTLPEndpoint), "protocol", proto)

	return &TracerProvider{shutdown: tp.Shutdown}
}

func passthroughProvider() *TracerProvider {
	return &TracerProvider{shutdown: func(context.Context) error { return nil }}
}

func globalIsNoop() bool {
	tp := otel.GetTracerProvider()
	if _, ok := tp.(noop.TracerProvider); ok {
		return true
	}
	// Knative wraps noop in its own TracerProvider when tracing-protocol is none/absent.
	if knativeProvider, ok := tp.(*knativetracing.TracerProvider); ok {
		_, isNoop := knativeProvider.TracerProvider.(noop.TracerProvider)
		return isNoop
	}
	return false
}

func protocolFromEnv() string {
	if v := os.Getenv(EnvOTLPTracesProtocol); v != "" {
		return v
	}
	if v := os.Getenv(EnvOTLPProtocol); v != "" {
		return v
	}
	return protocolGRPC
}

func newExporter(ctx context.Context, logger *zap.SugaredLogger, proto string) (sdktrace.SpanExporter, error) {
	endpoint := os.Getenv(EnvOTLPEndpoint)
	switch proto {
	case protocolHTTP:
		return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	case protocolGRPC:
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
	default:
		logger.Errorw("unsupported OTLP protocol; falling back to grpc", "protocol", proto)
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
	}
}

func (tp *TracerProvider) Shutdown(ctx context.Context) error {
	if tp.shutdown != nil {
		return tp.shutdown(ctx)
	}
	return nil
}

func samplerFromEnv(logger *zap.SugaredLogger) sdktrace.Sampler {
	name := os.Getenv(EnvTracesSampler)
	argStr := os.Getenv(EnvTracesSamplerArg)
	arg, err := strconv.ParseFloat(argStr, 64)
	if err != nil && argStr != "" {
		logger.Errorw("ignoring malformed sampler argument; defaulting to 0% sampling", "env", EnvTracesSamplerArg, "value", argStr)
	}
	if argStr == "" && (name == "traceidratio" || name == "parentbased_traceidratio") {
		logger.Infow("ratio sampler selected without "+EnvTracesSamplerArg+"; defaulting to 0% sampling", "env", EnvTracesSampler, "value", name)
	}
	switch name {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(arg)
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(arg))
	}
	logger.Warnw("unrecognized OTEL_TRACES_SAMPLER value; falling back to never sample", "value", name)
	return sdktrace.NeverSample()
}
