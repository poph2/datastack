// Package telemetry sets up OpenTelemetry tracing for a service.
package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// endpointEnv is the standard OTel variable the gRPC exporter reads its
// target from (for example http://otel-collector:4317).
const endpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

// ShutdownFunc flushes buffered spans and releases the exporter.
type ShutdownFunc func(context.Context) error

// SetupTracing installs the global tracer provider and the W3C trace-context
// propagator. Without the propagator, outgoing HTTP calls would not carry a
// traceparent header and every service would start its own trace.
//
// Exporting is configured through the standard OTEL_* environment variables.
// When OTEL_EXPORTER_OTLP_ENDPOINT is unset, spans are still created (so
// trace IDs show up in logs) but nothing is exported. This keeps `go run`
// usable without a collector.
func SetupTracing(ctx context.Context, serviceName string) (ShutdownFunc, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("building otel resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}

	if os.Getenv(endpointEnv) != "" {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("creating otlp trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}

	provider := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
