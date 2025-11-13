// Package trace is a shim to allow for easy migration from opencensus to
// opentelemetry.
package trace

import (
	"context"

	octrace "go.opencensus.io/trace"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var traceProvider *sdktrace.TracerProvider
var tracer trace.Tracer

type SpanData = octrace.SpanData
type ReadOnlySpan = sdktrace.ReadOnlySpan

// Span is a wrapper type that contains either a [trace.trace] or a
// [octrace.Span].
type Span trace.Span

func GetProvider() *sdktrace.TracerProvider {
	return traceProvider
}

func SetTracerWithExporter(exporter sdktrace.SpanExporter, resource *otelresource.Resource) {
	// r, err := otelresource.Merge(
	// 	otelresource.Default(),
	// 	otelresource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("rdk")),
	// )
	// if err != nil {
	// 	panic(err)
	// }
	traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
	)
	tracer = traceProvider.Tracer("go.viam.com/rdk")
}

func Shutdown(ctx context.Context) error {
	if traceProvider != nil {
		traceProvider.ForceFlush(ctx)
		return traceProvider.Shutdown(ctx)
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
