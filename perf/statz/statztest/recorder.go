package statztest

import (
	"github.com/edaniels/golog"
	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricexport"
	metrictest "go.opencensus.io/metric/test"
)

type recorder struct {
	reader   *metricexport.Reader
	exporter *metrictest.Exporter

	metricName string
}

func (r *recorder) getPoint(labelKeyValuePairs ...string) (metricdata.Point, bool) {
	labels := newStringSet(labelKeyValuePairs...)

	r.exporter.ReadAndExport()

	return r.exporter.GetPoint(r.metricName, labels)
}

type CounterRecorder struct {
	recorder
}

func (r *CounterRecorder) Value(labelKeyValuePairs ...string) int64 {
	p, ok := r.getPoint(labelKeyValuePairs...)
	if !ok {
		// This is expected before the metric is recorded the first time.
		return 0
	}
	return p.Value.(int64)
}

type DistributionRecorder struct {
	recorder
}

type DistributionRecord = metricdata.Distribution

func (r *DistributionRecorder) Value(labelKeyValuePairs ...string) DistributionRecord {
	p, ok := r.getPoint(labelKeyValuePairs...)
	if !ok {
		// This is expected before the metric is recorded the first time.
		return DistributionRecord{}
	}
	return *p.Value.(*DistributionRecord)
}

func NewCounterRecorder(metricName string) *CounterRecorder {
	metricReader := metricexport.NewReader()
	exporter := metrictest.NewExporter(metricReader)

	return &CounterRecorder{
		recorder: recorder{
			metricName: metricName,
			reader:     metricReader,
			exporter:   exporter,
		},
	}
}

func NewDistributionRecorder(metricName string) *DistributionRecorder {
	metricReader := metricexport.NewReader()
	exporter := metrictest.NewExporter(metricReader)

	return &DistributionRecorder{
		recorder: recorder{
			metricName: metricName,
			reader:     metricReader,
			exporter:   exporter,
		},
	}
}

func newStringSet(values ...string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}

	if len(values)%2 != 0 {
		golog.Global.Panic("Expected even number of keypairs")
	}

	set := make(map[string]string, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		set[values[i]] = values[i+1]
	}
	return set
}
