package utils

import (
	"context"
	"sync"
)

// StoppableWorkers is a collection of goroutines that can be stopped at a later time.
type StoppableWorkers struct {
	mu         sync.RWMutex
	ctx        context.Context
	cancelFunc func()

	workers sync.WaitGroup
}

// NewStoppableWorkers creates a new StoppableWorkers instance.
func NewStoppableWorkers() *StoppableWorkers {
	ctx, cancelFunc := context.WithCancel(context.Background())
	return &StoppableWorkers{ctx: ctx, cancelFunc: cancelFunc}
}

// AddWorker starts up a goroutine for the passed-in function. If you call this
// after calling Stop, it will return immediately without starting a new
// goroutine. Workers:
//
//   - MUST accept a `context.Context` and return nothing.
//   - MUST respond appropriately to errors on that context.
//   - MUST NOT add more workers to the `StoppableWorkers` group to which they
//     belong.
//
// Any `panic`s from workers will be `recover`ed and logged.
func (sw *StoppableWorkers) AddWorker(worker func(context.Context)) {
	// Read-lock to allow concurrent worker addition. The Stop method will write-lock.
	sw.mu.RLock()
	if sw.ctx.Err() != nil {
		return
	}
	sw.workers.Add(1)
	sw.mu.RUnlock()

	PanicCapturingGo(func() {
		defer sw.workers.Done()
		worker(sw.ctx)
	})
}

// Stop shuts down all the goroutines we started up.
func (sw *StoppableWorkers) Stop() {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.cancelFunc()
	sw.workers.Wait()
}
