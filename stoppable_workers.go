package utils

import (
	"context"
	"sync"
)

// StoppableWorkers is a collection of goroutines that can be stopped at a
// later time.
type StoppableWorkers struct {
	// Use a `sync.RWMutex` isntead of a `sync.Mutex` so that additions of new
	// workers do not lock with each other in any way. We want
	// as-fast-as-possible worker addition.
	mu         sync.RWMutex
	ctx        context.Context
	cancelFunc func()

	workers sync.WaitGroup
}

// NewStoppableWorkers creates a new StoppableWorkers instance. The instance's
// context will be derived from passed in context.
func NewStoppableWorkers(ctx context.Context) *StoppableWorkers {
	ctx, cancelFunc := context.WithCancel(ctx)
	return &StoppableWorkers{ctx: ctx, cancelFunc: cancelFunc}
}

// NewBackgroundStoppableWorkers creates a new StoppableWorkers instance. The
// instance's context will be derived from `context.Background()`. The passed
// in workers will be `Add`ed. Workers:
//
//   - MUST respond appropriately to errors on the context parameter.
//   - MUST NOT add more workers to the `StoppableWorkers` group to which
//     they belong.
//
// Any `panic`s from workers will be `recover`ed and logged.
func NewBackgroundStoppableWorkers(workers ...func(context.Context)) *StoppableWorkers {
	ctx, cancelFunc := context.WithCancel(context.Background())
	sw := &StoppableWorkers{ctx: ctx, cancelFunc: cancelFunc}
	for _, worker := range workers {
		worker := worker
		sw.Add(worker)
	}
	return sw
}

// Add starts up a goroutine for the passed-in function. Workers:
//
//   - MUST respond appropriately to errors on the context parameter.
//   - MUST NOT add more workers to the `StoppableWorkers` group to which
//     they belong.
//
// The worker will not be added if the StoppableWorkers instance has already
// been stopped. Any `panic`s from workers will be `recover`ed and logged.
func (sw *StoppableWorkers) Add(worker func(context.Context)) {
	// Read-lock to allow concurrent worker addition. The Stop method will write-lock.
	sw.mu.RLock()
	if sw.ctx.Err() != nil {
		sw.mu.RUnlock()
		return
	}
	sw.workers.Add(1)
	sw.mu.RUnlock()

	PanicCapturingGo(func() {
		defer sw.workers.Done()
		worker(sw.ctx)
	})
}

// Stop idempotently shuts down all the goroutines we started up.
func (sw *StoppableWorkers) Stop() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.ctx.Err() != nil {
		return
	}

	sw.cancelFunc()
	sw.workers.Wait()
}

// Context gets the context of the StoppableWorkers instance. Using this
// function is expected to be rare: usually you shouldn't need to interact with
// the context directly.
func (sw *StoppableWorkers) Context() context.Context {
	return sw.ctx
}
