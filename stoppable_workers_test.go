package utils_test

import (
	"context"
	"testing"
	"time"

	"go.viam.com/test"

	"go.viam.com/utils"
)

func TestStoppableWorkers(t *testing.T) {
	// Goleak checks from `VerifyTestMain` for `utils_test` should cause the
	// following tests to fail if `StoppableWorkers` leaks any goroutines.
	ctx := context.Background()

	t.Run("one worker", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		test.That(t, sw.Add(normalWorker), test.ShouldBeNil)
		sw.Stop()
	})

	t.Run("concurrent workers", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		concurrentWorker := func(ctx context.Context) {
			go normalWorker(ctx)
		}
		test.That(t, sw.Add(concurrentWorker), test.ShouldBeNil)
		test.That(t, sw.Add(concurrentWorker), test.ShouldBeNil)
		sw.Stop()
	})

	t.Run("nested workers", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		test.That(t, sw.Add(nestedWorkersWorker), test.ShouldBeNil)
		sw.Stop()
	})

	t.Run("panicking worker", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		// Both adding and stopping a panicking worker should cause no `panic`s.
		test.That(t, sw.Add(panickingWorker), test.ShouldBeNil)
		sw.Stop()
	})

	t.Run("already stopped", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		sw.Stop()
		test.That(t, sw.Add(normalWorker), test.ShouldBeError,
			utils.ErrStoppableWorkersAlreadyStopped)
		sw.Stop() // stopping twice should cause no `panic`
	})
}

func normalWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func nestedWorkersWorker(ctx context.Context) {
	nestedSW := utils.NewStoppableWorkers(ctx)
	nestedSW.Add(normalWorker)

	normalWorker(ctx)
}

func panickingWorker(_ context.Context) {
	panic("this worker panicked; ignore expected stack trace above")
}
