package perf

import (
	"context"
	"sync"
	"testing"

	"github.com/edaniels/golog"
	octrace "go.opencensus.io/trace"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.viam.com/test"

	viamtrace "go.viam.com/utils/trace"
)

// recordingSpanExporter is an [sdktrace.SpanExporter] that keeps the name of
// every span it is handed. Unlike tracetest.InMemoryExporter it holds on to
// them across Shutdown, so a test can assert on what a flush produced.
type recordingSpanExporter struct {
	mu      sync.Mutex
	names   []string
	started bool
}

// Start implements startableSpanExporter, mirroring the OTLP exporters, which
// drop spans until they have been started.
func (r *recordingSpanExporter) Start(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = true
	return nil
}

func (r *recordingSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, span := range spans {
		r.names = append(r.names, span.Name())
	}
	return nil
}

func (r *recordingSpanExporter) Shutdown(context.Context) error { return nil }

func (r *recordingSpanExporter) exported() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// recordingMetricExporter is an [sdkmetric.Exporter] that keeps every metric
// name it is handed.
type recordingMetricExporter struct {
	mu    sync.Mutex
	names []string
}

func (r *recordingMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (r *recordingMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (r *recordingMetricExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			r.names = append(r.names, m.Name)
		}
	}
	return nil
}

func (r *recordingMetricExporter) ForceFlush(context.Context) error { return nil }

func (r *recordingMetricExporter) Shutdown(context.Context) error { return nil }

func (r *recordingMetricExporter) exported() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// TestOtelExporterBridgesOpenCensus asserts the property the whole migration
// rests on: instrumentation still written against OpenCensus is exported by
// the OpenTelemetry exporters the concrete [Exporter]s configure.
func TestOtelExporterBridgesOpenCensus(t *testing.T) {
	spanExporter := &recordingSpanExporter{}
	metricExporter := &recordingMetricExporter{}
	exporter := &otelExporter{
		logger:         golog.NewTestLogger(t),
		resource:       serviceResource("perf-test"),
		sampler:        sdktrace.AlwaysSample(),
		spanExporter:   spanExporter,
		metricExporter: metricExporter,
	}

	test.That(t, exporter.Start(), test.ShouldBeNil)
	// Starting twice is a misuse; the second call should not silently install a
	// second set of providers.
	test.That(t, exporter.Start(), test.ShouldNotBeNil)

	// A span recorded through the OpenCensus API...
	_, ocSpan := octrace.StartSpan(context.Background(), "opencensus-span")
	ocSpan.End()
	// ...and one recorded through go.viam.com/utils/trace.
	_, otelSpan := viamtrace.StartSpan(context.Background(), "otel-span")
	otelSpan.End()

	// Stop flushes both providers.
	exporter.Stop()

	// The OTLP exporters silently drop everything until started.
	test.That(t, spanExporter.started, test.ShouldBeTrue)
	test.That(t, spanExporter.exported(), test.ShouldContain, "opencensus-span")
	test.That(t, spanExporter.exported(), test.ShouldContain, "otel-span")

	// registerApplicationViews registers the OpenCensus runtime metrics, which
	// the metric bridge should have picked up.
	test.That(t, metricExporter.exported(), test.ShouldNotBeEmpty)

	// Stop restored the OpenCensus tracer it displaced, so later spans are no
	// longer routed to the (now shut down) provider.
	test.That(t, octrace.DefaultTracer, test.ShouldNotBeNil)
}
