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
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.uber.org/zap"
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
	if os.Getenv(EnvOTLPEndpoint) == "" {
		logger.Info("OTLP endpoint not configured, using noop tracer provider")
		return noopProvider()
	}
	if os.Getenv(EnvTracesSampler) == "" {
		logger.Info("OTEL_TRACES_SAMPLER not set, tracing disabled (set explicitly to opt in)")
		return noopProvider()
	}

	proto := protocolFromEnv()
	exporter, err := newExporter(context.Background(), logger, proto)
	if err != nil {
		logger.Errorw("failed to create OTLP exporter, using noop tracer provider", "error", err)
		return noopProvider()
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

func noopProvider() *TracerProvider {
	return &TracerProvider{shutdown: func(context.Context) error { return nil }}
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
		logger.Errorw("ignoring malformed sampler argument", "env", EnvTracesSamplerArg, "value", argStr)
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
