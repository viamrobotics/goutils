package utils_test

import (
	"bytes"
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
		sw.Add(normalWorker)
		sw.Stop()
		ctx := sw.Context()
		test.That(t, ctx, test.ShouldNotBeNil)
		test.That(t, ctx.Err(), test.ShouldBeError, context.Canceled)
	})

	t.Run("one worker background constructor", func(t *testing.T) {
		sw := utils.NewBackgroundStoppableWorkers(normalWorker)
		sw.Stop()
		ctx := sw.Context()
		test.That(t, ctx, test.ShouldNotBeNil)
		test.That(t, ctx.Err(), test.ShouldBeError, context.Canceled)
	})

	t.Run("heavy workers", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		sw.Add(heavyWorker)
		sw.Add(heavyWorker)
		sw.Add(heavyWorker)

		// Sleep for half a second to let heavy workers do work.
		time.Sleep(500 * time.Millisecond)
		sw.Stop()
	})

	t.Run("concurrent workers", func(t *testing.T) {
		ints := make(chan int)
		writeWorker := func(ctx context.Context) {
			var count int
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					ints <- count
				}
			}
		}
		var receivedInts []int
		readWorker := func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case i := <-ints:
					receivedInts = append(receivedInts, i)
				}
			}
		}

		sw := utils.NewBackgroundStoppableWorkers(writeWorker, readWorker)
		// Sleep for a second to let concurrent workers do work.
		time.Sleep(500 * time.Millisecond)
		sw.Stop()

		test.That(t, len(receivedInts), test.ShouldBeGreaterThan, 0)
	})

	t.Run("nested workers", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		sw.Add(nestedWorkersWorker)
		sw.Stop()
	})

	t.Run("panicking worker", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		// Both adding and stopping a panicking worker should cause no `panic`s.
		sw.Add(panickingWorker)
		sw.Stop()
	})

	t.Run("already stopped", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		sw.Stop()
		sw.Add(normalWorker) // adding after Stop should cause no `panic`
		sw.Stop()            // stopping twice should cause no `panic`
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

// like `normalWorker`, but writes and reads bytes from a buffer.
func heavyWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
			var buffer bytes.Buffer
			data := make([]byte, 10000)
			buffer.Write(data)
			readData := make([]byte, buffer.Len())
			buffer.Read(readData)
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
