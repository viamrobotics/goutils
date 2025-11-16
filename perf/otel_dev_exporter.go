package perf

// based on "go.opencensus.io/examples/exporter"

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricexport"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.viam.com/utils"
	"go.viam.com/utils/trace"
)

// OtelDevelopmentExporter exports metrics and span to log file.
type OtelDevelopmentExporter struct {
	mu             sync.Mutex
	shutdown       bool
	children       map[string][]myOtelSpanInfo
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

// ExportSpans implements trace.SpanExporter.
func (e *OtelDevelopmentExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shutdown {
		return nil
	}

	for _, sd := range spans {
		length := (sd.EndTime().UnixNano() - sd.StartTime().UnixNano()) / (1000 * 1000)
		myinfo := fmt.Sprintf("%s %d ms", sd.Name(), length)

		for _, a := range sd.Attributes() {
			myinfo = myinfo + " " + string(a.Key) + ":" + a.Value.Emit()
		}

		spanId := sd.SpanContext().SpanID()
		spanID := hex.EncodeToString(spanId[:])
		parentSpanId := sd.Parent().SpanID()
		parentSpanID := hex.EncodeToString(parentSpanId[:])

		if !reZero.MatchString(parentSpanID) {
			e.children[parentSpanID] = append(e.children[parentSpanID], myOtelSpanInfo{myinfo, spanID, sd})
			continue
		}

		wd := walkData{}
		e.recurse(&myOtelSpanInfo{myinfo, spanID, sd}, []string{}, &wd)
		wd.output(e.outputWriter)
	}
	return nil
}

// Shutdown implements [sdktrace.SpanExporter].
func (e *OtelDevelopmentExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.shutdown = true
	e.mu.Unlock()
	if !e.o.metricsDisabled {
		e.ir.Stop()
	}
	return nil
}

// OtelDevelopmentExporterOptions provides options for DevelopmentExporter.
type OtelDevelopmentExporterOptions struct {
	// reportingInterval is a time interval between two successive metrics
	// export.
	reportingInterval time.Duration

	// metricsDisabled determines if metrics reporting is disabled or not.
	metricsDisabled bool

	// tracesDisabled determines if trace reporting is disabled or not.
	tracesDisabled bool
}

type myOtelSpanInfo struct {
	toPrint string
	id      string
	Data    sdktrace.ReadOnlySpan
}

// NewOtelDevelopmentExporter creates a new log exporter.
func NewOtelDevelopmentExporter() *OtelDevelopmentExporter {
	return NewOtelDevelopmentExporterWithOptions(OtelDevelopmentExporterOptions{
		reportingInterval: 10 * time.Second,
	})
}

// NewOtelDevelopmentExporterWithOptions creates a new log exporter with the given options.
func NewOtelDevelopmentExporterWithOptions(options OtelDevelopmentExporterOptions) *OtelDevelopmentExporter {
	return &OtelDevelopmentExporter{
		children:     map[string][]myOtelSpanInfo{},
		reader:       metricexport.NewReader(),
		o:            options,
		outputWriter: os.Stdout,
	}
}

// Start starts the metric and span data exporter.
func (e *OtelDevelopmentExporter) Start() error {
	if err := registerApplicationViews(); err != nil {
		return err
	}

	if !e.o.tracesDisabled {
		trace.SetTracerWithExporters(resource.Empty(), e)
	}
	if !e.o.metricsDisabled {
		e.initReaderOnce.Do(func() {
			var err error
			e.ir, err = metricexport.NewIntervalReader(&metricexport.Reader{}, e)
			utils.UncheckedError(err)
		})
		e.ir.ReportingInterval = e.o.reportingInterval
		return e.ir.Start()
	}
	return nil
}

// Stop stops the metric and span data exporter.
func (e *OtelDevelopmentExporter) Stop() {
	trace.Shutdown(context.Background())
}

// Close closes any files that were opened for logging.
func (e *OtelDevelopmentExporter) Close() {
}

// ExportMetrics exports to log.
func (e *OtelDevelopmentExporter) ExportMetrics(ctx context.Context, metrics []*metricdata.Metric) error {
	metricsTransform := make(map[string]interface{}, len(metrics))

	transformPoint := func(point metricdata.Point) interface{} {
		switch v := point.Value.(type) {
		case *metricdata.Distribution:
			dv := v
			return map[string]interface{}{
				"count":      dv.Count,
				"sum":        dv.Sum,
				"sum_sq_dev": dv.SumOfSquaredDeviation,
			}
		default:
			return point.Value
		}
	}

	for _, metric := range metrics {
		if len(metric.TimeSeries) == 0 {
			continue
		}
		if len(metric.Descriptor.LabelKeys) == 0 {
			if len(metric.TimeSeries) == 0 || len(metric.TimeSeries[0].Points) == 0 {
				continue
			}
			metricsTransform[metric.Descriptor.Name] = transformPoint(metric.TimeSeries[0].Points[0])
			continue
		}

		var pointVals []interface{}
		for _, ts := range metric.TimeSeries {
			if len(ts.Points) == 0 {
				continue
			}
			labels := make([][]string, 0, len(metric.Descriptor.LabelKeys))
			for idx, key := range metric.Descriptor.LabelKeys {
				labels = append(labels, []string{key.Key, ts.LabelValues[idx].Value})
			}
			if len(labels) == 1 {
				pointVals = append(pointVals, map[string]interface{}{
					strings.Join(labels[0], ":"): transformPoint(ts.Points[0]),
				})
				continue
			}
			pointVals = append(pointVals, map[string]interface{}{
				"labels": labels,
				"value":  transformPoint(ts.Points[0]),
			})
		}
		metricsTransform[metric.Descriptor.Name] = pointVals
	}
	md, err := json.MarshalIndent(metricsTransform, "", "  ")
	if err != nil {
		return err
	}
	log.Println(string(md))
	return nil
}

func (e *OtelDevelopmentExporter) recurse(currSpan *myOtelSpanInfo, callerPath []string, wd *walkData) {
	// Get the accumulator for this
	myPath := wd.get(callerPath, currSpan.Data.Name())
	myPath.count++
	myPath.timeNanos += currSpan.Data.EndTime().UnixNano() - currSpan.Data.StartTime().UnixNano()

	// We incremented our counters. Now walk all of our children spans and do the same.
	children := e.children[currSpan.id]
	for idx := range children {
		e.recurse(&children[idx], myPath.spanChain, wd)
	}

	if !e.deleteDisabled {
		delete(e.children, currSpan.id)
	}
}
