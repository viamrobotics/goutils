package perf

import (
	"testing"
	"testing/synctest"
	"time"

	"go.opencensus.io/trace"
	"go.viam.com/test"
)

func rootParams(name string, traceIDByte byte) trace.SamplingParameters {
	var traceID trace.TraceID
	for i := range traceID {
		traceID[i] = traceIDByte
	}
	return trace.SamplingParameters{
		TraceID: traceID,
		Name:    name,
	}
}

func TestRouteRateLimitingSampler(t *testing.T) {
	t.Run("never samples when perSec <= 0", func(t *testing.T) {
		for _, perSec := range []float64{0, -0.0001, -1, -1e9} {
			sampler := NewRootNameRateLimitingSampler(perSec)
			for i := range 100 {
				dec := sampler(rootParams("foo", byte(i)))
				test.That(t, dec.Sample, test.ShouldBeFalse)
			}
		}
	})

	t.Run("defers to parent context for non-root spans", func(t *testing.T) {
		// Use a high perSec so the sampler would otherwise sample root spans.
		sampler := NewRootNameRateLimitingSampler(1e6)
		nonZeroSpanID := trace.SpanID{1}

		params := trace.SamplingParameters{
			ParentContext: trace.SpanContext{
				SpanID:       nonZeroSpanID,
				TraceOptions: 1, // sampled bit set
			},
			Name: "foo",
		}
		test.That(t, sampler(params).Sample, test.ShouldBeTrue)

		params.ParentContext.TraceOptions = 0
		test.That(t, sampler(params).Sample, test.ShouldBeFalse)

		// Name has never been seen as a root before — should still defer to parent.
		params.Name = "never-seen-as-root"
		params.ParentContext.TraceOptions = 0
		test.That(t, sampler(params).Sample, test.ShouldBeFalse)

		params.ParentContext.TraceOptions = 1
		test.That(t, sampler(params).Sample, test.ShouldBeTrue)
	})

	t.Run("first root span for each unique name is always sampled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			sampler := NewRootNameRateLimitingSampler(1)

			for _, name := range []string{"a", "b", "c", "d", "/foo/bar", ""} {
				dec := sampler(rootParams(name, 0xff))
				test.That(t, dec.Sample, test.ShouldBeTrue)

				// Second call for the same name at the same instant should not
				// sample given the tiny perSec — sanity-checks that the
				// "always sample" only applies to the first occurrence.
				dec = sampler(rootParams(name, 0xff))
				test.That(t, dec.Sample, test.ShouldBeFalse)

				// Advance fake time past 1/perSec so the next call samples again.
				time.Sleep(2 * time.Second)
				dec = sampler(rootParams(name, 0))
				test.That(t, dec.Sample, test.ShouldBeTrue)
			}
		})
	})
}
