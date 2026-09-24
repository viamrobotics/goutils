package perf

import (
	"math"
	"runtime/metrics"
	"sync"
	"time"

	"go.viam.com/utils"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

const (
	schedLatencyMetric = "/sched/latencies:seconds"
	// Matches the cloud exporter's default reporting period, so each point covers one whole window.
	runtimeSampleInterval = time.Minute
)

var (
	// Nanoseconds, because most waits fall in the histogram's sub-microsecond buckets.
	schedLatencyGauge = statz.NewGauge1[string]("process/sched_latency_ns", statz.MetricConfig{
		Description: "Time goroutines spent runnable before running, over the last sample window",
		Unit:        units.Nanoseconds,
		Labels: []statz.Label{
			{Name: "quantile", Description: "p50 / p99 / max"},
		},
	})
)

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

// runtimeSampler publishes the Go scheduler's wait quantiles once per window, alongside the
// runtime metrics runmetrics already exports.
type runtimeSampler struct {
	samples    []metrics.Sample
	prevCounts []uint64

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newRuntimeSampler() *runtimeSampler {
	return &runtimeSampler{
		samples: []metrics.Sample{{Name: schedLatencyMetric}},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *runtimeSampler) sample() {
	metrics.Read(s.samples)
	if s.samples[0].Value.Kind() == metrics.KindFloat64Histogram {
		hist := s.samples[0].Value.Float64Histogram()
		window := schedLatencyQuantiles(s.prevCounts, hist)
		schedLatencyGauge.Set("p50", window.p50.Nanoseconds())
		schedLatencyGauge.Set("p99", window.p99.Nanoseconds())
		schedLatencyGauge.Set("max", window.longest.Nanoseconds())
		// Read reuses the histogram's storage next time, so keep a copy rather than the slice.
		s.prevCounts = append(s.prevCounts[:0], hist.Counts...)
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
