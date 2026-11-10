package perf

import (
	"math"
	"runtime/metrics"
	"sync"
	"time"

	"go.opencensus.io/metric/metricdata"

	"go.viam.com/utils"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

const (
	schedLatencyMetric      = "/sched/latencies:seconds"
	schedLatencyName        = "process/sched_latency_ns"
	schedLatencyDescription = "Time goroutines spent runnable before running, over the last sample window"
	// Sample period for exporters that receive pushed view data instead of reading producers.
	runtimeSampleInterval = time.Minute
)

// Nanoseconds, because most waits fall in the histogram's sub-microsecond buckets.
var schedLatencyGauge = statz.NewGauge1[string](schedLatencyName, statz.MetricConfig{
	Description: schedLatencyDescription,
	Unit:        units.Nanoseconds,
	Labels: []statz.Label{
		{Name: "quantile", Description: "p50 / p99 / max"},
	},
})

// The descriptor the gauge's view exports: OpenCensus reports any unit but 1, ms and By as 1.
var schedLatencyDescriptor = metricdata.Descriptor{
	Name:        schedLatencyName,
	Description: schedLatencyDescription,
	Unit:        metricdata.UnitDimensionless,
	Type:        metricdata.TypeGaugeInt64,
	LabelKeys:   []metricdata.LabelKey{{Key: "quantile"}},
}

// schedLatencyWindow is one window's p50, p99 and longest scheduler wait.
type schedLatencyWindow struct {
	p50, p99, longest time.Duration
}

// schedLatencyQuantiles reduces the cumulative scheduler histogram to the window since
// prevCounts; nil prevCounts means since process start.
func schedLatencyQuantiles(prevCounts []uint64, hist *metrics.Float64Histogram) schedLatencyWindow {
	delta := make([]uint64, len(hist.Counts))
	var total uint64
	for i, count := range hist.Counts {
		if i < len(prevCounts) {
			count -= prevCounts[i]
		}
		delta[i] = count
		total += count
	}
	var window schedLatencyWindow
	if total == 0 {
		return window
	}
	var seen uint64
	var p50Set, p99Set bool
	for i, count := range delta {
		if count == 0 {
			continue
		}
		seen += count
		upper := bucketUpperBound(hist.Buckets, i)
		if !p50Set && seen*2 >= total {
			window.p50, p50Set = upper, true
		}
		if !p99Set && seen*100 >= total*99 {
			window.p99, p99Set = upper, true
		}
		window.longest = upper
	}
	return window
}

// bucketUpperBound is a bucket's upper boundary as a duration, or its lower boundary for the
// open-ended last bucket.
func bucketUpperBound(buckets []float64, i int) time.Duration {
	upper := buckets[i+1]
	if math.IsInf(upper, 1) {
		upper = buckets[i]
	}
	// Rounded: boundaries are float seconds, and truncating 128e-9 * 1e9 can yield 127ns.
	return time.Duration(math.Round(upper * float64(time.Second)))
}

// schedLatencyWindows reads the runtime's cumulative scheduler histogram; each next call
// returns the quantiles since the previous one.
type schedLatencyWindows struct {
	mu         sync.Mutex
	samples    []metrics.Sample
	prevCounts []uint64
}

func newSchedLatencyWindows() *schedLatencyWindows {
	return &schedLatencyWindows{samples: []metrics.Sample{{Name: schedLatencyMetric}}}
}

// next reports false if the runtime does not provide the scheduler histogram.
func (w *schedLatencyWindows) next() (schedLatencyWindow, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	metrics.Read(w.samples)
	if w.samples[0].Value.Kind() != metrics.KindFloat64Histogram {
		return schedLatencyWindow{}, false
	}
	hist := w.samples[0].Value.Float64Histogram()
	window := schedLatencyQuantiles(w.prevCounts, hist)
	// Read reuses the histogram's storage next time, so keep a copy rather than the slice.
	w.prevCounts = append(w.prevCounts[:0], hist.Counts...)
	return window, true
}

// schedLatencyProducer computes the window when an exporter reads it, so each export covers
// exactly the time since the previous one. Every Read advances the window: give it one reader.
type schedLatencyProducer struct {
	windows *schedLatencyWindows
}

func newSchedLatencyProducer() *schedLatencyProducer {
	return &schedLatencyProducer{windows: newSchedLatencyWindows()}
}

// Read implements metricproducer.Producer.
func (p *schedLatencyProducer) Read() []*metricdata.Metric {
	window, ok := p.windows.next()
	if !ok {
		return nil
	}
	now := time.Now()
	series := func(quantile string, d time.Duration) *metricdata.TimeSeries {
		return &metricdata.TimeSeries{
			LabelValues: []metricdata.LabelValue{metricdata.NewLabelValue(quantile)},
			Points:      []metricdata.Point{metricdata.NewInt64Point(now, d.Nanoseconds())},
		}
	}
	return []*metricdata.Metric{{
		Descriptor: schedLatencyDescriptor,
		TimeSeries: []*metricdata.TimeSeries{
			series("p50", window.p50), series("p99", window.p99), series("max", window.longest),
		},
	}}
}

// runtimeSampler sets the scheduler latency gauge on a timer, for exporters that receive
// pushed view data and so never call a producer.
type runtimeSampler struct {
	windows *schedLatencyWindows

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newRuntimeSampler() *runtimeSampler {
	return &runtimeSampler{
		windows: newSchedLatencyWindows(),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *runtimeSampler) sample() {
	if window, ok := s.windows.next(); ok {
		schedLatencyGauge.Set("p50", window.p50.Nanoseconds())
		schedLatencyGauge.Set("p99", window.p99.Nanoseconds())
		schedLatencyGauge.Set("max", window.longest.Nanoseconds())
	}
}

// startRuntimeSampler samples every interval until Stop is called; a non-positive interval
// falls back to runtimeSampleInterval.
func startRuntimeSampler(interval time.Duration) *runtimeSampler {
	if interval <= 0 {
		interval = runtimeSampleInterval
	}
	s := newRuntimeSampler()
	utils.ManagedGo(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.sample()
			}
		}
	}, func() { close(s.done) })
	return s
}

// Stop ends sampling and waits for the loop to exit. It is safe to call more than once or on nil.
func (s *runtimeSampler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
}
