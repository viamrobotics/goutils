package perf

import (
	"math"
	"runtime/metrics"
	"slices"
	"testing"
	"time"

	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricproducer"
	"go.opencensus.io/stats/view"
	"go.viam.com/test"

	"go.viam.com/utils/perf/statz/statztest"
)

func TestSchedLatencyQuantiles(t *testing.T) {
	// Buckets: [0,1µs) [1µs,10µs) [10µs,100µs) [100µs,1ms) [1ms,+Inf)
	hist := &metrics.Float64Histogram{
		Buckets: []float64{0, 1e-6, 1e-5, 1e-4, 1e-3, math.Inf(1)},
		Counts:  []uint64{50, 40, 9, 1, 0},
	}

	t.Run("since process start", func(t *testing.T) {
		test.That(t, schedLatencyQuantiles(nil, hist), test.ShouldResemble,
			schedLatencyWindow{p50: time.Microsecond, p99: 100 * time.Microsecond, longest: time.Millisecond})
	})

	t.Run("window since the previous read", func(t *testing.T) {
		// Delta is 0, 0, 9, 1, 0: half of it is reached in the third bucket, all of it in the fourth.
		test.That(t, schedLatencyQuantiles([]uint64{50, 40, 0, 0, 0}, hist), test.ShouldResemble,
			schedLatencyWindow{p50: 100 * time.Microsecond, p99: time.Millisecond, longest: time.Millisecond})
	})

	t.Run("empty window", func(t *testing.T) {
		test.That(t, schedLatencyQuantiles(hist.Counts, hist), test.ShouldResemble, schedLatencyWindow{})
	})

	t.Run("sub-microsecond boundaries keep nanosecond precision", func(t *testing.T) {
		// The runtime's low buckets: [0,64ns) [64ns,128ns) [128ns,192ns), boundaries in float seconds.
		fine := &metrics.Float64Histogram{
			Buckets: []float64{0, 64e-9, 128e-9, 192e-9, math.Inf(1)},
			Counts:  []uint64{10, 80, 10, 0},
		}
		test.That(t, schedLatencyQuantiles(nil, fine), test.ShouldResemble,
			schedLatencyWindow{p50: 128 * time.Nanosecond, p99: 192 * time.Nanosecond, longest: 192 * time.Nanosecond})
	})

	t.Run("open-ended last bucket reports its lower boundary", func(t *testing.T) {
		overflow := &metrics.Float64Histogram{Buckets: hist.Buckets, Counts: []uint64{0, 0, 0, 0, 3}}
		test.That(t, schedLatencyQuantiles(nil, overflow), test.ShouldResemble,
			schedLatencyWindow{p50: time.Millisecond, p99: time.Millisecond, longest: time.Millisecond})
	})
}

func TestSchedLatencyProducerRead(t *testing.T) {
	producer := newSchedLatencyProducer()
	producer.Read()
	read := producer.Read()

	test.That(t, read, test.ShouldHaveLength, 1)
	test.That(t, read[0].Descriptor, test.ShouldResemble, schedLatencyDescriptor)
	values := map[string]int64{}
	for _, series := range read[0].TimeSeries {
		test.That(t, series.LabelValues, test.ShouldHaveLength, 1)
		test.That(t, series.Points, test.ShouldHaveLength, 1)
		values[series.LabelValues[0].Value] = series.Points[0].Value.(int64)
	}
	test.That(t, values, test.ShouldHaveLength, 3)
	test.That(t, values["max"], test.ShouldBeGreaterThanOrEqualTo, values["p99"])
	test.That(t, values["p99"], test.ShouldBeGreaterThanOrEqualTo, values["p50"])
	test.That(t, len(producer.windows.prevCounts), test.ShouldBeGreaterThan, 0)
}

// Cloud Monitoring keys the metric on its descriptor, so the producer must export the same one
// the gauge's view does.
func TestSchedLatencyDescriptorMatchesGaugeView(t *testing.T) {
	schedLatencyGauge.Set("p50", 1)
	// The view worker records asynchronously; RetrieveData queues behind the Set.
	_, err := view.RetrieveData(schedLatencyName)
	test.That(t, err, test.ShouldBeNil)
	var fromView *metricdata.Descriptor
	for _, producer := range metricproducer.GlobalManager().GetAll() {
		if _, ours := producer.(*schedLatencyProducer); ours {
			continue
		}
		for _, m := range producer.Read() {
			if m.Descriptor.Name == schedLatencyName {
				fromView = &m.Descriptor
			}
		}
	}
	test.That(t, fromView, test.ShouldNotBeNil)
	test.That(t, *fromView, test.ShouldResemble, schedLatencyDescriptor)
}

func TestDevelopmentExporterRegistersSchedLatencyProducer(t *testing.T) {
	exporter := NewDevelopmentExporterWithOptions(DevelopmentExporterOptions{
		ReportingInterval: time.Hour,
		TracesDisabled:    true,
	}).(*developmentExporter)
	registered := func() bool {
		return slices.Contains(metricproducer.GlobalManager().GetAll(), metricproducer.Producer(exporter.sched))
	}

	test.That(t, exporter.Start(), test.ShouldBeNil)
	test.That(t, registered(), test.ShouldBeTrue)
	exporter.Stop()
	test.That(t, registered(), test.ShouldBeFalse)
}

func TestRuntimeSamplerSample(t *testing.T) {
	latency := statztest.NewGaugeRecorder("process/sched_latency_ns")

	sampler := newRuntimeSampler()
	sampler.sample()
	sampler.sample()

	test.That(t, len(sampler.windows.prevCounts), test.ShouldBeGreaterThan, 0)
	test.That(t, latency.Value("quantile", "max"), test.ShouldBeGreaterThanOrEqualTo, latency.Value("quantile", "p99"))
	test.That(t, latency.Value("quantile", "p99"), test.ShouldBeGreaterThanOrEqualTo, latency.Value("quantile", "p50"))
}

func TestRuntimeSamplerStop(t *testing.T) {
	sampler := startRuntimeSampler(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	sampler.Stop()
	sampler.Stop()
	var none *runtimeSampler
	none.Stop()
}
