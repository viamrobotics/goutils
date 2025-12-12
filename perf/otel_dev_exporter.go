package perf

// based on "go.opencensus.io/examples/exporter"

import (
	"context"
	"encoding/hex"
	"io"
	"os"
	"sync"
	"time"

	"github.com/samber/lo"
	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricexport"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"go.viam.com/utils"
)

// OtelDevelopmentExporter exports metrics and spans to log file.
type OtelDevelopmentExporter struct {
	mu             sync.Mutex
	children       map[string][]*myOtelSpanInfo
	reader         *metricexport.Reader
	ir             *metricexport.IntervalReader
	initReaderOnce sync.Once
	o              OtelDevelopmentExporterOptions

	// For testing. Disable deleting from `children` such that a test can walk over `children` again
	// to recreate span information.
	deleteDisabled bool

	// For testing. By default will be set to stdout.
	outputWriter io.Writer
}

// ExportSpans implements [sdktrace.SpanExporter].
func (e *OtelDevelopmentExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	spanInfo := lo.Map(spans, func(span sdktrace.ReadOnlySpan, _ int) *myOtelSpanInfo {
		return spanInfoFromROSpan(span)
	})
	e.exportSpans(spanInfo)
	return nil
}

// ExportOTLPSpans displays spans that have already been serialized in OTLP format.
func (e *OtelDevelopmentExporter) ExportOTLPSpans(ctx context.Context, spans []*tracepb.Span) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	spanInfo := lo.Map(spans, func(span *tracepb.Span, _ int) *myOtelSpanInfo {
		return spanInfoFromOTLPSpan(span)
	})
	e.exportSpans(spanInfo)
	return nil
}

// Internal implementation to handle exporting spans after they have been
// converted to a common struct. Callers must hold OtelDevelopmentExporter.mu.
func (e *OtelDevelopmentExporter) exportSpans(spans []*myOtelSpanInfo) {
	for _, sd := range spans {
		if !reZero.MatchString(sd.parentSpanID) {
			e.children[sd.parentSpanID] = append(e.children[sd.parentSpanID], sd)
			continue
		}

		wd := walkData{}
		e.recurse(sd, []string{}, &wd)
		wd.output(e.outputWriter)
	}
}

// Shutdown implements [sdktrace.SpanExporter].
func (e *OtelDevelopmentExporter) Shutdown(ctx context.Context) error {
	// Noop. Theoretically we could flush any leftover spans here but would need
	// to figure out 1) how to format the output to make clear they are missing a
	// root span and 2) how not to reprint previously exported spans when
	// OtelDevelopmentExporter.deleteDisabled is true.
	return nil
}

// OtelDevelopmentExporterOptions provides options for
// [OtelDevelopmentExporter].
type OtelDevelopmentExporterOptions struct {
	// ReportingInterval is a time interval between two successive metrics
	// export.
	ReportingInterval time.Duration

	// Out sets the [io.Writer] that formatted traces will be written to. If this
	// is nil then [os.Stdout] will be used.
	Out io.Writer
}

type myOtelSpanInfo struct {
	traceID      string
	spanID       string
	parentSpanID string
	name         string
	startTime    time.Time
	endTime      time.Time
}

var emptyTraceID oteltrace.TraceID

func traceIDToStr(id []byte) string {
	if len(id) == 0 {
		id = emptyTraceID[:]
	}
	return hex.EncodeToString(id)
}

var emptySpanID oteltrace.SpanID

func spanIDToStr(id []byte) string {
	if len(id) == 0 {
		id = emptySpanID[:]
	}
	return hex.EncodeToString(id)
}

func spanInfoFromROSpan(span sdktrace.ReadOnlySpan) *myOtelSpanInfo {
	traceID := span.SpanContext().TraceID()
	spanID := span.SpanContext().SpanID()
	parentSpanID := span.Parent().SpanID()
	return &myOtelSpanInfo{
		traceID:      traceIDToStr(traceID[:]),
		spanID:       spanIDToStr(spanID[:]),
		parentSpanID: spanIDToStr(parentSpanID[:]),
		name:         span.Name(),
		startTime:    span.StartTime(),
		endTime:      span.EndTime(),
	}
}

func spanInfoFromOTLPSpan(span *tracepb.Span) *myOtelSpanInfo {
	return &myOtelSpanInfo{
		traceID:      traceIDToStr(span.GetTraceId()),
		spanID:       spanIDToStr(span.GetSpanId()),
		parentSpanID: spanIDToStr(span.GetParentSpanId()),
		name:         span.GetName(),
		startTime:    time.Unix(0, int64(span.GetStartTimeUnixNano())),
		endTime:      time.Unix(0, int64(span.GetEndTimeUnixNano())),
	}
}

// NewOtelDevelopmentExporter creates a new log exporter.
func NewOtelDevelopmentExporter() *OtelDevelopmentExporter {
	return NewOtelDevelopmentExporterWithOptions(OtelDevelopmentExporterOptions{
		ReportingInterval: 10 * time.Second,
	})
}

// NewOtelDevelopmentExporterWithOptions creates a new log exporter with the given options.
func NewOtelDevelopmentExporterWithOptions(options OtelDevelopmentExporterOptions) *OtelDevelopmentExporter {
	out := options.Out
	if out == nil {
		out = os.Stdout
	}
	return &OtelDevelopmentExporter{
		children:     map[string][]*myOtelSpanInfo{},
		reader:       metricexport.NewReader(),
		o:            options,
		outputWriter: out,
	}
}

// StartMetrics starts the metric exporter. To use the span exporter, register this
// as an exporter on the relevant [sdktrace.TracerProvider].
func (e *OtelDevelopmentExporter) StartMetrics() error {
	if err := registerApplicationViews(); err != nil {
		return err
	}

	e.initReaderOnce.Do(func() {
		var err error
		e.ir, err = metricexport.NewIntervalReader(&metricexport.Reader{}, e)
		utils.UncheckedError(err)
	})
	e.ir.ReportingInterval = e.o.ReportingInterval
	return e.ir.Start()
}

// StopMetrics stops the metrics exporter. To stop exporting spans, shut down the
// trace provider or remove this exporter from it.
func (e *OtelDevelopmentExporter) StopMetrics() {
	e.ir.Stop()
}

// Close closes any files that were opened for logging.
func (e *OtelDevelopmentExporter) Close() {
}

// ExportMetrics exports to log.
func (e *OtelDevelopmentExporter) ExportMetrics(ctx context.Context, metrics []*metricdata.Metric) error {
	return exportMetrics(metrics)
}

func (e *OtelDevelopmentExporter) recurse(currSpan *myOtelSpanInfo, callerPath []string, wd *walkData) {
	// Get the accumulator for this
	myPath := wd.get(callerPath, currSpan.name)
	myPath.count++
	myPath.timeNanos += currSpan.endTime.UnixNano() - currSpan.startTime.UnixNano()

	// We incremented our counters. Now walk all of our children spans and do the same.
	children := e.children[currSpan.spanID]
	for idx := range children {
		e.recurse(children[idx], myPath.spanChain, wd)
	}

	if !e.deleteDisabled {
		delete(e.children, currSpan.spanID)
	}
}
