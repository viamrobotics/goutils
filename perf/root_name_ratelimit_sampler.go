package perf

import (
	"sync"
	"time"

	"go.opencensus.io/trace"
	"golang.org/x/time/rate"
)

// NewRootNameRateLimitingSampler creates a new [trace.Sampler] that samples
// perSec traces per second per unique root span name. The first encountered
// root span of each name is always sampled.
func NewRootNameRateLimitingSampler(perSec float64) trace.Sampler {
	if perSec <= 0 {
		return trace.NeverSample()
	}
	period := time.Second * time.Duration(1/perSec)

	limitersByName := &smap[string, *rate.Limiter]{}
	return func(sp trace.SamplingParameters) trace.SamplingDecision {
		// Only apply to root spans, otherwise defer to the parent's decision.
		var zeroSpanID trace.SpanID
		if sp.ParentContext.SpanID != zeroSpanID {
			return trace.SamplingDecision{Sample: sp.ParentContext.IsSampled()}
		}

		// Try to load first and only allocate a new limiter if we miss to avoid
		// generating GC pressure on every request.
		limiter, present := limitersByName.Load(sp.Name)
		if !present {
			limiter, _ = limitersByName.LoadOrStore(sp.Name, rate.NewLimiter(rate.Every(period), 1))
		}
		return trace.SamplingDecision{Sample: limiter.Allow()}
	}
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
