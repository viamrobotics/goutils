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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricexport"
	"go.opencensus.io/trace"

	"go.viam.com/utils"
)

// developmentExporter exports metrics and span to log file.
type developmentExporter struct {
	mu             sync.Mutex
	children       map[string][]mySpanInfo
	reader         *metricexport.Reader
	ir             *metricexport.IntervalReader
	initReaderOnce sync.Once
	o              DevelopmentExporterOptions

	// For testing. Disable deleting from `children` such that a test can walk over `children` again
	// to recreate span information.
	deleteDisabled bool

	// For testing. By default will be set to stdout.
	outputWriter io.Writer
}

// DevelopmentExporterOptions provides options for DevelopmentExporter.
type DevelopmentExporterOptions struct {
	// ReportingInterval is a time interval between two successive metrics
	// export.
	ReportingInterval time.Duration

	// MetricsDisabled determines if metrics reporting is disabled or not.
	MetricsDisabled bool

	// TracesDisabled determines if trace reporting is disabled or not.
	TracesDisabled bool
}

type mySpanInfo struct {
	toPrint string
	id      string
	Data    *trace.SpanData
}

var reZero = regexp.MustCompile(`^0+$`)

// NewDevelopmentExporter creates a new log exporter.
func NewDevelopmentExporter() Exporter {
	return NewDevelopmentExporterWithOptions(DevelopmentExporterOptions{
		ReportingInterval: 10 * time.Second,
	})
}

// NewDevelopmentExporterWithOptions creates a new log exporter with the given options.
func NewDevelopmentExporterWithOptions(options DevelopmentExporterOptions) Exporter {
	return &developmentExporter{
		children:     map[string][]mySpanInfo{},
		reader:       metricexport.NewReader(),
		o:            options,
		outputWriter: os.Stdout,
	}
}

// Start starts the metric and span data exporter.
func (e *developmentExporter) Start() error {
	if err := registerApplicationViews(); err != nil {
		return err
	}

	if !e.o.TracesDisabled {
		trace.RegisterExporter(e)
		trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	}
	if !e.o.MetricsDisabled {
		e.initReaderOnce.Do(func() {
			var err error
			e.ir, err = metricexport.NewIntervalReader(&metricexport.Reader{}, e)
			utils.UncheckedError(err)
		})
		e.ir.ReportingInterval = e.o.ReportingInterval
		return e.ir.Start()
	}
	return nil
}

// Stop stops the metric and span data exporter.
func (e *developmentExporter) Stop() {
	if !e.o.TracesDisabled {
		trace.UnregisterExporter(e)
	}
	if !e.o.MetricsDisabled {
		e.ir.Stop()
	}
}

// Close closes any files that were opened for logging.
func (e *developmentExporter) Close() {
}

// ExportMetrics exports to log.
func (e *developmentExporter) ExportMetrics(ctx context.Context, metrics []*metricdata.Metric) error {
	return exportMetrics(metrics)
}

// Internal implementation currently shared between [developmentExporter] and
// [OtelDevelopmentExporter].
func exportMetrics(metrics []*metricdata.Metric) error {
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

// walkData accumulates all of the sub-spans when walking a completed span.
type walkData struct {
	paths []spanPath
}

// get is called with `parents(currSpan), currSpan`. So if span `A` calls span `B` calls span `C`:
//
//	caller: ["A", "B"]
//	callee: "C"
func (wd *walkData) get(caller []string, callee string) *spanPath {
	if wd.paths == nil {
		// This is the root span. `caller` is assumed to be empty. Initialize the paths with the
		// callee.
		wd.paths = []spanPath{{
			spanChain: []string{callee},
		}}

		return &wd.paths[0]
	}

	// First, see if we have an exact match.
	for idx, path := range wd.paths {
		// Below reads as "the prefix of the span chain" is equal to the `caller`.
		if len(caller)+1 == len(path.spanChain) && slices.Equal(caller, path.spanChain[:len(caller)]) &&
			// and the tail of the span chain is equal to the `callee`.
			path.spanChain[len(caller)] == callee {
			return &wd.paths[idx]
		}
	}

	// Otherwise, we are a new `spanChain`. Add to the tally of `paths`.
	pathCopy := make([]string, len(caller)+1)
	copy(pathCopy, caller)
	pathCopy[len(caller)] = callee
	wd.paths = append(wd.paths, spanPath{
		spanChain: pathCopy,
	})

	return &wd.paths[len(wd.paths)-1]
}

// spanPath represents an entire "invocation chain" from the root span.
type spanPath struct {
	// If Span A calls Span B calls Span C, we get `['A", "B", "C"]`. Where `C` is the span/function
	// the count/timing information is representing.
	spanChain []string
	count     int64
	timeNanos int64
}

func (sp *spanPath) funcName() string {
	return sp.spanChain[len(sp.spanChain)-1]
}

func (sp *spanPath) totalTime() time.Duration {
	return time.Duration(sp.timeNanos)
}

func (sp *spanPath) averageTime() time.Duration {
	// `sp.count` must be at least "1".
	return time.Duration(sp.timeNanos / sp.count)
}

func (wd *walkData) output(writer io.Writer) {
	// For padding, we calculate the maximum length of the indented span/function name for each row.
	maxLength := 0
	for _, spanPath := range wd.paths {
		// We indent each span/function name by two spaces per call depth from the root span. Plus
		// one for the trailing colon.
		thisLength := 2*len(spanPath.spanChain) + len(spanPath.funcName()) + 1
		maxLength = max(maxLength, thisLength)
	}

	for _, spanPath := range wd.paths {
		indentedName := fmt.Sprintf("%v%v:", strings.Repeat("  ", len(spanPath.spanChain)-1), spanPath.funcName())
		trailingSpaces := strings.Repeat(" ", maxLength-len(indentedName))
		_, err := fmt.Fprintf(writer, "%v%v\tCalls: %5d\tTotal time: %-13v\tAverage time: %v\n",
			indentedName, trailingSpaces,
			spanPath.count, spanPath.totalTime(), spanPath.averageTime())
		utils.UncheckedError(err)
	}
}

func (e *developmentExporter) recurse(currSpan *mySpanInfo, callerPath []string, wd *walkData) {
	// Get the accumulator for this
	myPath := wd.get(callerPath, currSpan.Data.Name)
	myPath.count++
	myPath.timeNanos += currSpan.Data.EndTime.UnixNano() - currSpan.Data.StartTime.UnixNano()

	// We incremented our counters. Now walk all of our children spans and do the same.
	children := e.children[currSpan.id]
	for idx := range children {
		e.recurse(&children[idx], myPath.spanChain, wd)
	}

	if !e.deleteDisabled {
		delete(e.children, currSpan.id)
	}
}

// ExportSpan exports a SpanData to log.
func (e *developmentExporter) ExportSpan(sd *trace.SpanData) {
	e.mu.Lock()
	defer e.mu.Unlock()

	length := (sd.EndTime.UnixNano() - sd.StartTime.UnixNano()) / (1000 * 1000)
	myinfo := fmt.Sprintf("%s %d ms", sd.Name, length)

	if sd.Annotations != nil {
		for _, a := range sd.Annotations {
			myinfo = myinfo + " " + a.Message
		}
	}

	spanID := hex.EncodeToString(sd.SpanID[:])
	parentSpanID := hex.EncodeToString(sd.ParentSpanID[:])

	if !reZero.MatchString(parentSpanID) {
		e.children[parentSpanID] = append(e.children[parentSpanID], mySpanInfo{myinfo, spanID, sd})
		return
	}

	wd := walkData{}
	e.recurse(&mySpanInfo{myinfo, spanID, sd}, []string{}, &wd)
	wd.output(e.outputWriter)
}
