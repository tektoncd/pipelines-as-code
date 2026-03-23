package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	TracerName      = "pipelines-as-code"
	EnvOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
)

type TracerProvider struct {
	provider trace.TracerProvider
	shutdown func(context.Context) error
	logger   *zap.SugaredLogger
}

// New returns a noop tracer provider when the OTLP endpoint is not configured.
func New(ctx context.Context, logger *zap.SugaredLogger) (*TracerProvider, error) {
	endpoint := os.Getenv(EnvOTLPEndpoint)
	if endpoint == "" {
		logger.Debug("OTLP endpoint not configured, using noop tracer provider")
		return &TracerProvider{
			provider: trace.NewNoopTracerProvider(),
			shutdown: func(context.Context) error { return nil },
			logger:   logger,
		}, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(), // TODO: make TLS configurable
	)
	if err != nil {
		logger.Warnf("failed to create OTLP exporter: %v, using noop tracer provider", err)
		return &TracerProvider{
			provider: trace.NewNoopTracerProvider(),
			shutdown: func(context.Context) error { return nil },
			logger:   logger,
		}, nil
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(TracerName),
		),
	)
	if err != nil {
		logger.Warnf("failed to create resource: %v", err)
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	logger.Infof("tracing initialized with OTLP endpoint: %s", endpoint)

	return &TracerProvider{
		provider: tp,
		shutdown: tp.Shutdown,
		logger:   logger,
	}, nil
}

func (tp *TracerProvider) Tracer() trace.Tracer {
	return tp.provider.Tracer(TracerName)
}

func (tp *TracerProvider) Shutdown(ctx context.Context) error {
	if tp.shutdown != nil {
		return tp.shutdown(ctx)
	}
	return nil
}

// IsEnabled reports whether a real (non-noop) provider is active.
func (tp *TracerProvider) IsEnabled() bool {
	_, ok := tp.provider.(*sdktrace.TracerProvider)
	return ok
}
