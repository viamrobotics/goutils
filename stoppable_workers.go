package utils

import (
	"context"
	"errors"
	"sync"
)

var StoppableWorkersAlreadyStopped = errors.New("cannot add worker: already stopped")

// StoppableWorkers is a collection of goroutines that can be stopped at a
// later time.
type StoppableWorkers struct {
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

// Add starts up a goroutine for the passed-in function. Workers:
//
//   - MUST respond appropriately to errors on the context parameter.
//   - MUST NOT add more workers to the `StoppableWorkers` group to which
//     they belong.
//
// Any `panic`s from workers will be `recover`ed and logged.
func (sw *StoppableWorkers) Add(worker func(context.Context)) error {
	// Read-lock to allow concurrent worker addition. The Stop method will write-lock.
	sw.mu.RLock()
	if sw.ctx.Err() != nil {
		sw.mu.RUnlock()
		return StoppableWorkersAlreadyStopped
	}
	sw.workers.Add(1)
	sw.mu.RUnlock()

	PanicCapturingGo(func() {
		defer sw.workers.Done()
		worker(sw.ctx)
	})
	return nil
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
