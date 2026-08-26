package perf

import (
	"context"
	"errors"
	"sync"
	"time"

	octrace "go.opencensus.io/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/bridge/opencensus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.viam.com/utils"
	viamtrace "go.viam.com/utils/trace"
)

// otelExporter is the shared implementation behind every production
// [Exporter]. It owns the OpenTelemetry SDK plumbing — a tracer provider, an
// optional meter provider, and the OpenCensus bridges — so that the concrete
// exporters only have to supply backend specific OTLP/Cloud exporters.
//
// The OpenCensus bridges are what keep the still-OpenCensus instrumentation in
// this module (see [NewGrpcStatsHandler], [WrapHTTPHandlerForStats] and
// go.viam.com/utils/perf/statz) reporting after the move to OpenTelemetry:
// spans started with go.opencensus.io/trace are recorded by the tracer
// provider below, and views registered with go.opencensus.io/stats/view are
// collected by the metric reader below.
//
// See https://opentelemetry.io/docs/specs/otel/compatibility/opencensus/#migration-path.
type otelExporter struct {
	// ctx is used for exporter creation/shutdown and defaults to
	// [context.Background] when unset.
	ctx    context.Context
	logger utils.ZapCompatibleLogger

	resource *resource.Resource
	sampler  sdktrace.Sampler

	spanExporter sdktrace.SpanExporter
	batchOpts    []sdktrace.BatchSpanProcessorOption

	// metricExporter is optional; when nil no metrics are exported.
	metricExporter sdkmetric.Exporter
	// metricInterval overrides the default reporting interval when non-zero.
	metricInterval time.Duration

	mu            sync.Mutex
	meterProvider *sdkmetric.MeterProvider
	// prevOCTracer is the OpenCensus tracer displaced by the trace bridge, kept
	// so Stop can put it back.
	prevOCTracer octrace.Tracer
	started      bool
}

var _ Exporter = &otelExporter{}

// startableSpanExporter is implemented by span exporters that need to be
// started before they will accept spans, such as the OTLP exporters.
type startableSpanExporter interface {
	Start(ctx context.Context) error
}

func (e *otelExporter) context() context.Context {
	if e.ctx == nil {
		return context.Background()
	}
	return e.ctx
}

// Start implements [Exporter]. It installs global OpenTelemetry providers —
// including the one held by go.viam.com/utils/trace — so only one [Exporter]
// may be running at a time; starting a second one replaces the providers
// installed by the first, along with any exporters registered with
// [viamtrace.AddExporters].
func (e *otelExporter) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return errors.New("exporter already started")
	}

	if err := registerApplicationViews(); err != nil {
		return err
	}

	// Surface OpenTelemetry SDK and bridge errors through our logger rather
	// than the default handler, which writes to the standard logger.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		e.logger.Errorw("opentelemetry error", "error", err)
	}))

	if se, ok := e.spanExporter.(startableSpanExporter); ok {
		if err := se.Start(e.context()); err != nil {
			return err
		}
	}

	if err := viamtrace.SetProvider(e.context(),
		sdktrace.WithResource(e.resource),
		sdktrace.WithSampler(e.sampler),
		sdktrace.WithBatcher(e.spanExporter, e.batchOpts...),
	); err != nil {
		return err
	}
	// Point both the OpenTelemetry global and the OpenCensus global at the
	// provider so instrumentation written against either API is exported.
	otel.SetTracerProvider(viamtrace.GetProvider())
	e.prevOCTracer = octrace.DefaultTracer
	opencensus.InstallTraceBridge()

	if e.metricExporter != nil {
		readerOpts := []sdkmetric.PeriodicReaderOption{
			sdkmetric.WithProducer(opencensus.NewMetricProducer()),
		}
		if e.metricInterval > 0 {
			readerOpts = append(readerOpts, sdkmetric.WithInterval(e.metricInterval))
		}
		e.meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(e.resource),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(e.metricExporter, readerOpts...)),
		)
		otel.SetMeterProvider(e.meterProvider)
	}

	e.started = true
	return nil
}

// Stop implements [Exporter]. It shuts down the providers installed by
// [otelExporter.Start], flushing anything still pending.
func (e *otelExporter) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	ctx := e.context()
	if e.meterProvider != nil {
		if err := e.meterProvider.Shutdown(ctx); err != nil {
			e.logger.Errorw("failed to shut down meter provider", "error", err)
		}
		e.meterProvider = nil
	}
	// Shutting down the tracer provider flushes pending spans and shuts down
	// the span exporter.
	if err := viamtrace.Shutdown(ctx); err != nil {
		e.logger.Errorw("failed to shut down tracer provider", "error", err)
	}
	octrace.DefaultTracer = e.prevOCTracer
	e.prevOCTracer = nil
	e.started = false
}

// serviceResource builds the OpenTelemetry resource describing this process.
// The service name is what shows up as the service in Cloud Trace, Jaeger and
// the OpenTelemetry Collector.
func serviceResource(serviceName string, extra ...attribute.KeyValue) *resource.Resource {
	attrs := make([]attribute.KeyValue, 0, len(extra)+1)
	if serviceName != "" {
		attrs = append(attrs, semconv.ServiceName(serviceName))
	}
	attrs = append(attrs, extra...)
	return resource.NewSchemaless(attrs...)
}
