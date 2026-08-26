package perf

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.viam.com/test"
)

func rootParams(name string, traceIDByte byte) sdktrace.SamplingParameters {
	var traceID oteltrace.TraceID
	for i := range traceID {
		traceID[i] = traceIDByte
	}
	return sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       traceID,
		Name:          name,
	}
}

// childParams returns sampling parameters for a span whose parent has already
// made the given sampling decision.
func childParams(name string, parentSampled bool) sdktrace.SamplingParameters {
	var flags oteltrace.TraceFlags
	if parentSampled {
		flags = oteltrace.FlagsSampled
	}
	parent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1},
		SpanID:     oteltrace.SpanID{1},
		TraceFlags: flags,
	})
	return sdktrace.SamplingParameters{
		ParentContext: oteltrace.ContextWithSpanContext(context.Background(), parent),
		TraceID:       parent.TraceID(),
		Name:          name,
	}
}

func sampled(result sdktrace.SamplingResult) bool {
	return result.Decision == sdktrace.RecordAndSample
}

func TestRouteRateLimitingSampler(t *testing.T) {
	t.Run("never samples when perSec <= 0", func(t *testing.T) {
		for _, perSec := range []float64{0, -0.0001, -1, -1e9} {
			sampler := NewRootNameRateLimitingSampler(perSec)
			for i := range 100 {
				test.That(t, sampled(sampler.ShouldSample(rootParams("foo", byte(i)))), test.ShouldBeFalse)
			}
		}
	})

	t.Run("defers to parent context for non-root spans", func(t *testing.T) {
		// Use a high perSec so the sampler would otherwise sample root spans.
		sampler := NewRootNameRateLimitingSampler(1e6)

		test.That(t, sampled(sampler.ShouldSample(childParams("foo", true))), test.ShouldBeTrue)
		test.That(t, sampled(sampler.ShouldSample(childParams("foo", false))), test.ShouldBeFalse)

		// Name has never been seen as a root before — should still defer to parent.
		test.That(t, sampled(sampler.ShouldSample(childParams("never-seen-as-root", false))), test.ShouldBeFalse)
		test.That(t, sampled(sampler.ShouldSample(childParams("never-seen-as-root", true))), test.ShouldBeTrue)
	})

	t.Run("first root span for each unique name is always sampled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			sampler := NewRootNameRateLimitingSampler(1)

			for _, name := range []string{"a", "b", "c", "d", "/foo/bar", ""} {
				test.That(t, sampled(sampler.ShouldSample(rootParams(name, 0xff))), test.ShouldBeTrue)

				// Second call for the same name at the same instant should not
				// sample given the tiny perSec — sanity-checks that the
				// "always sample" only applies to the first occurrence.
				test.That(t, sampled(sampler.ShouldSample(rootParams(name, 0xff))), test.ShouldBeFalse)

				// Advance fake time past 1/perSec so the next call samples again.
				time.Sleep(2 * time.Second)
				test.That(t, sampled(sampler.ShouldSample(rootParams(name, 0))), test.ShouldBeTrue)
			}
		})
	})
}
