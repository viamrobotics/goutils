package perf

import (
	"math"
	"runtime/metrics"
	"testing"
	"time"

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

	t.Run("open-ended last bucket reports its lower boundary", func(t *testing.T) {
		overflow := &metrics.Float64Histogram{Buckets: hist.Buckets, Counts: []uint64{0, 0, 0, 0, 3}}
		test.That(t, schedLatencyQuantiles(nil, overflow), test.ShouldResemble,
			schedLatencyWindow{p50: time.Millisecond, p99: time.Millisecond, longest: time.Millisecond})
	})
}

func TestRuntimeSamplerSample(t *testing.T) {
	latency := statztest.NewGaugeRecorder("process/sched_latency_us")
	busy := statztest.NewGaugeRecorder("process/cpu_busy_percent")

	sampler := newRuntimeSampler()
	sampler.sample()
	sampler.sample()

	test.That(t, len(sampler.prevCounts), test.ShouldBeGreaterThan, 0)
	test.That(t, latency.Value("quantile", "max"), test.ShouldBeGreaterThanOrEqualTo, latency.Value("quantile", "p99"))
	test.That(t, latency.Value("quantile", "p99"), test.ShouldBeGreaterThanOrEqualTo, latency.Value("quantile", "p50"))
	test.That(t, busy.Value(), test.ShouldBeBetweenOrEqual, int64(0), int64(100))
}

func TestRuntimeSamplerStop(t *testing.T) {
	sampler := startRuntimeSampler(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	sampler.Stop()
	sampler.Stop()
	var none *runtimeSampler
	none.Stop()
}
