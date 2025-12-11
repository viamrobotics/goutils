// Package trace is a shim to allow for easy migration from opencensus to opentelemetry.
package trace

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	otelresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Struct to group all the global state we care about.
type globalTraceState struct {
	tracerProvider trace.TracerProvider
	exporter       *mutableBatcher
	tracer         trace.Tracer
}

// Store the global state in a sync pointer and protect it with a mutex to
// optimize for the read paths.
var (
	globalTraceStateDataMu sync.Mutex
	globalTraceStateData   atomic.Pointer[globalTraceState]
)

func init() {
	globalTraceStateDataMu.Lock()
	defer globalTraceStateDataMu.Unlock()

	globals := &globalTraceState{}
	globals.tracerProvider = noop.NewTracerProvider()
	// This will never actually be used because of the noop provider but
	// initialize it anyway to avoid potential NPEs.
	globals.exporter = newMutableBatcher()
	globals.tracer = globals.tracerProvider.Tracer("unconfigured")
	globalTraceStateData.Store(globals)
}

// SetProvider creates a new [sdktrace.TracerProvider] and stores it + a tracer
// named "unconfigured" in the global state. See [SetTracerWithExporters] to
// create a tracer with a useful name and install exporters for spans.
func SetProvider(ctx context.Context, opts ...sdktrace.TracerProviderOption) error {
	globalTraceStateDataMu.Lock()
	defer globalTraceStateDataMu.Unlock()

	exporter := newMutableBatcher()
	opts = append(opts, sdktrace.WithBatcher(exporter))
	traceProvider := sdktrace.NewTracerProvider(opts...)
	tracer := traceProvider.Tracer("unconfigured")

	prev := globalTraceStateData.Swap(&globalTraceState{
		exporter:       exporter,
		tracerProvider: traceProvider,
		tracer:         tracer,
	})

	if prev != nil {
		if sdkProvider, ok := prev.tracerProvider.(*sdktrace.TracerProvider); ok {
			return sdkProvider.Shutdown(ctx)
		}
	}
	return nil
}

type mutableBatcher struct {
	mu       sync.Mutex
	children atomic.Pointer[[]sdktrace.SpanExporter]
}

func newMutableBatcher() *mutableBatcher {
	batcher := &mutableBatcher{}
	batcher.children.Store(&[]sdktrace.SpanExporter{})
	return batcher
}

// ExportSpans implements trace.SpanExporter.
func (m *mutableBatcher) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var err error
	childrenPtr := m.children.Load()
	if childrenPtr == nil {
		return nil
	}
	for _, c := range *childrenPtr {
		err = errors.Join(err, c.ExportSpans(ctx, spans))
	}
	return err
}

func (m *mutableBatcher) addExporters(exporters ...sdktrace.SpanExporter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	children := slices.Clone(*m.children.Load())
	for _, ex := range exporters {
		if slices.Contains(children, ex) {
			continue
		}
		children = append(children, ex)
	}
	m.children.Store(&children)
}

func (m *mutableBatcher) removeExporters(exporters ...sdktrace.SpanExporter) []sdktrace.SpanExporter {
	m.mu.Lock()
	defer m.mu.Unlock()
	children := slices.Clone(*m.children.Load())
	removed := []sdktrace.SpanExporter{}
	children = slices.DeleteFunc(
		children,
		func(e sdktrace.SpanExporter) bool {
			if slices.Contains(exporters, e) {
				removed = append(removed, e)
				return true
			}
			return false
		},
	)
	m.children.Store(&children)
	return removed
}

func (m *mutableBatcher) clearExporters() []sdktrace.SpanExporter {
	m.mu.Lock()
	defer m.mu.Unlock()
	emptyChildren := []sdktrace.SpanExporter{}
	children := *m.children.Swap(&emptyChildren)
	return children
}

// Shutdown implements trace.SpanExporter.
func (m *mutableBatcher) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	children := *m.children.Load()
	for _, c := range children {
		err = errors.Join(err, c.Shutdown(ctx))
	}
	return err
}

// Span is a type alias to [trace.Span].
type Span = trace.Span

// GetProvider returns the [trace.TracerProvider] configured by the last call to
// [SetTracerWithExporters].
func GetProvider() trace.TracerProvider {
	globals := globalTraceStateData.Load()
	return globals.tracerProvider
}

// SetTracerWithExporters creates a [trace.TracerProvider] and stores it for
// global use.
func SetTracerWithExporters(resource *otelresource.Resource, exporters ...sdktrace.SpanExporter) {
	globalTraceStateDataMu.Lock()
	defer globalTraceStateDataMu.Unlock()

	globals := globalTraceStateData.Load()
	globalsClone := *globals
	globalsClone.exporter.addExporters(exporters...)
	globals.tracer = globals.tracerProvider.Tracer("go.viam.com/rdk")
	globalTraceStateData.Swap(&globalsClone)
}

// ClearExporters clears all span exporters that were previously added with
// [AddExporters]. It returns the removed exporters as a slice. It does not
// call [sdktrace.SpanExporter.Shutdown] on the removed exporters.
func ClearExporters() []sdktrace.SpanExporter {
	return globalTraceStateData.Load().exporter.clearExporters()
}

// AddExporters adds the provided exporters to the global
// [sdktrace.TracerProvider].
func AddExporters(exporters ...sdktrace.SpanExporter) {
	globalTraceStateData.Load().exporter.addExporters(exporters...)
}

// RemoveExporters any span exporters in exporters that were previously added
// with [AddExporters]. It returns the removed exporters as a slice. It does
// not call [sdktrace.SpanExporter.Shutdown] on the removed exporters.
func RemoveExporters(exporters ...sdktrace.SpanExporter) []sdktrace.SpanExporter {
	return globalTraceStateData.Load().exporter.removeExporters(exporters...)
}

// Shutdown shuts down the global [trace.TracerProvider] created with
// [SetTracerWithExporters].
func Shutdown(ctx context.Context) error {
	if sdkProvider, ok := globalTraceStateData.Load().tracerProvider.(*sdktrace.TracerProvider); ok {
		return sdkProvider.Shutdown(ctx)
	}
	return nil
}

// StartSpan is a wrapper around [trace.Tracer.Start].
func StartSpan(ctx context.Context, name string, o ...trace.SpanStartOption) (context.Context, Span) {
	return globalTraceStateData.Load().tracer.Start(ctx, name)
}

// FromContext is a wrapper around [trace.FromContext].
func FromContext(ctx context.Context) Span {
	return trace.SpanFromContext(ctx)
}

// NewContext is a wrapper around [trace.ContextWithSpan].
func NewContext(ctx context.Context, span Span) context.Context {
	return trace.ContextWithSpan(ctx, span)
}
