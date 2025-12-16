// Package trace is a shim to allow for easy migration from opencensus to opentelemetry.
package trace

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const instrumentationPackage = "go.viam.com/utils/trace"

// Struct to group all the global state we care about.
type globalTraceState struct {
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer
	exporter       *mutableExporter
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
	globals.tracer = globals.tracerProvider.Tracer(instrumentationPackage)
	globalTraceStateData.Store(globals)
}

// SetProvider creates a new [sdktrace.TracerProvider] and stores it + a tracer
// named "go.viam.com/utils/trace" in the global state. To dynamically configure
// exporters, do _not_ pass exporters with [sdktrace.WithBatcher] or similar
// here; instead use [AddExporters].
func SetProvider(ctx context.Context, opts ...sdktrace.TracerProviderOption) error {
	globalTraceStateDataMu.Lock()
	defer globalTraceStateDataMu.Unlock()

	exporter := newMutableBatcher()
	opts = append(opts, sdktrace.WithBatcher(exporter))
	traceProvider := sdktrace.NewTracerProvider(opts...)
	tracer := traceProvider.Tracer(instrumentationPackage)

	prev := globalTraceStateData.Swap(&globalTraceState{
		tracerProvider: traceProvider,
		tracer:         tracer,
		exporter:       exporter,
	})

	if prev != nil {
		if sdkProvider, ok := prev.tracerProvider.(*sdktrace.TracerProvider); ok {
			return sdkProvider.Shutdown(ctx)
		}
	}
	return nil
}

// mutableExporter contains one or more [sdktrace.SpanExporter]s that it
// forwards all spans to. It is thread safe to modify this list of exporters at
// runtime using the provided methods.
type mutableExporter struct {
	mu       sync.Mutex
	children atomic.Pointer[[]sdktrace.SpanExporter]
}

func newMutableBatcher() *mutableExporter {
	batcher := &mutableExporter{}
	batcher.children.Store(&[]sdktrace.SpanExporter{})
	return batcher
}

// ExportSpans implements [sdktrace.SpanExporter].
func (m *mutableExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
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

func (m *mutableExporter) addExporters(exporters ...sdktrace.SpanExporter) {
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

func (m *mutableExporter) removeExporters(exporters ...sdktrace.SpanExporter) []sdktrace.SpanExporter {
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

func (m *mutableExporter) clearExporters() []sdktrace.SpanExporter {
	m.mu.Lock()
	defer m.mu.Unlock()
	emptyChildren := []sdktrace.SpanExporter{}
	children := *m.children.Swap(&emptyChildren)
	return children
}

// Shutdown implements [sdktrace.SpanExporter].
func (m *mutableExporter) Shutdown(ctx context.Context) error {
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

// GetProvider returns the [trace.TracerProvider] configured by the last call
// to [SetProvider].
func GetProvider() trace.TracerProvider {
	globals := globalTraceStateData.Load()
	return globals.tracerProvider
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
// [SetProvider].
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
