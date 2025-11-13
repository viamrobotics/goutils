// Package trace is a shim to allow for easy migration from opencensus to
// opentelemetry.
package trace

import (
	"context"

	otelresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var (
	traceProvider trace.TracerProvider = noop.NewTracerProvider()
	tracer        trace.Tracer         = traceProvider.Tracer("unconfigured")
)

type Span = trace.Span

func GetProvider() trace.TracerProvider {
	return traceProvider
}

func SetTracerWithExporters(resource *otelresource.Resource, exporters ...sdktrace.SpanExporter) {
	opts := make([]sdktrace.TracerProviderOption, 0, 1+len(exporters))
	opts = append(opts, sdktrace.WithResource(resource))
	for _, exp := range exporters {
		opts = append(opts, sdktrace.WithBatcher(exp))
	}
	traceProvider = sdktrace.NewTracerProvider(opts...)
	tracer = traceProvider.Tracer("go.viam.com/rdk")
}

func Shutdown(ctx context.Context) error {
	if sdkTraceProvider, ok := traceProvider.(*sdktrace.TracerProvider); ok {
		return sdkTraceProvider.Shutdown(ctx)
	}
	return nil
}

// StartSpan is a wrapper aronud [trace.Tracer.Start].
func StartSpan(ctx context.Context, name string, o ...trace.SpanStartOption) (context.Context, Span) {
	ctx, span := tracer.Start(ctx, name)
	return ctx, span
}

// FromContext is a wrapper around [trace.FromContext].
func FromContext(ctx context.Context) Span {
	return trace.SpanFromContext(ctx)
}

// NewContext is a wrapper around [trace.ContextWithSpan].
func NewContext(ctx context.Context, span Span) context.Context {
	return trace.ContextWithSpan(ctx, span)
}
