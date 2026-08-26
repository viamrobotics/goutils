package perf

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// NewRootNameRateLimitingSampler creates a new [sdktrace.Sampler] that samples
// perSec traces per second per unique root span name. The first encountered
// root span of each name is always sampled. Non-root spans defer to the
// sampling decision of their parent.
func NewRootNameRateLimitingSampler(perSec float64) sdktrace.Sampler {
	if perSec <= 0 {
		return sdktrace.ParentBased(sdktrace.NeverSample())
	}
	return sdktrace.ParentBased(&rootNameRateLimitingSampler{
		// - [sync.Map] is optimized for a write-once-read-many access pattern, so
		//   just storing timestamps directly in the values would work but likely lead
		//   to suboptimal performance
		// - [sync.Pointer] could be used to hold a pointer to a [time.Time] but
		//   would add GC pressure on some code paths that can otherwise pass by value
		//   by using an int64
		nextSampleNanosByName: &smap[string, *atomic.Int64]{},
		intervalNanos:         time.Duration(float64(time.Second) * (1 / perSec)).Nanoseconds(),
		perSec:                perSec,
	})
}

// rootNameRateLimitingSampler is the root sampler behind
// [NewRootNameRateLimitingSampler]. It is only ever consulted for root spans;
// [sdktrace.ParentBased] handles deferring to the parent otherwise.
type rootNameRateLimitingSampler struct {
	nextSampleNanosByName *smap[string, *atomic.Int64]
	intervalNanos         int64
	perSec                float64
}

// ShouldSample implements [sdktrace.Sampler].
func (s *rootNameRateLimitingSampler) ShouldSample(params sdktrace.SamplingParameters) sdktrace.SamplingResult {
	// Try to load first and only allocate a new sync.Int64 if we miss to avoid
	// generating GC pressure on every request.
	nextSampleAtomic, present := s.nextSampleNanosByName.Load(params.Name)
	nowNanos := time.Now().UnixNano()
	var sample bool
	if present {
		nextSample := nextSampleAtomic.Load()
		if nowNanos >= nextSample {
			// If we decided to sample there's still a chance we lost the race w/
			// another goroutine. Discard our positive result if something else has
			// already overwritten the atomic.
			nextNanos := nowNanos + s.intervalNanos
			sample = nextSampleAtomic.CompareAndSwap(nextSample, nextNanos)
		}
	} else {
		// This is our first time seeing a root span with this particular name.
		// Assume we should sample.
		nowPtr := &atomic.Int64{}
		// Only on the first request, randomly distribute the next sample time
		// anywhere within the configured interval. The intent is to guard
		// against situations where most span names are registered at the same
		// time during startup and we are left with bursts of CPU and network
		// usage every time the sampling interval hits.
		//nolint:gosec
		nextNanos := nowNanos + int64(float64(s.intervalNanos)*rand.Float64())
		nowPtr.Store(nextNanos)
		// If another goroutine beat us to the first sampling, discard our
		// positive result.
		_, lostRace := s.nextSampleNanosByName.LoadOrStore(params.Name, nowPtr)
		sample = !lostRace
	}

	decision := sdktrace.Drop
	if sample {
		decision = sdktrace.RecordAndSample
	}
	return sdktrace.SamplingResult{
		Decision:   decision,
		Tracestate: oteltrace.SpanContextFromContext(params.ParentContext).TraceState(),
	}
}

// Description implements [sdktrace.Sampler].
func (s *rootNameRateLimitingSampler) Description() string {
	return fmt.Sprintf("RootNameRateLimitingSampler{%g per second}", s.perSec)
}

// smap is an alias to [sync.Map] with generic type parameters.
type smap[K comparable, V any] sync.Map

// CompareAndSwap is an alias to [sync.Map.CompareAndSwap].
func (m *smap[K, V]) CompareAndSwap(key K, oldVal, newVal V) bool {
	return (*sync.Map)(m).CompareAndSwap(key, oldVal, newVal)
}

// Load is an alias to [sync.Map.Load].
func (m *smap[K, V]) Load(key K) (V, bool) {
	v, ok := (*sync.Map)(m).Load(key)
	if !ok {
		var zero V
		return zero, ok
	}
	return v.(V), ok
}

// LoadOrStore is an alias to [sync.Map.LoadOrStore].
func (m *smap[K, V]) LoadOrStore(key K, value V) (V, bool) {
	v, stored := (*sync.Map)(m).LoadOrStore(key, value)
	return v.(V), stored
}
