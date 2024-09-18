package utils

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go.viam.com/test"
)

func TestStoppableWorkers(t *testing.T) {
	// Goleak checks from `VerifyTestMain` for `utils_test` should cause the
	// following tests to fail if `StoppableWorkers` leaks any goroutines.
	ctx := context.Background()

	t.Run("one worker", func(t *testing.T) {
		sw := NewStoppableWorkers(ctx)
		sw.Add(normalWorker)
		sw.Stop()
		ctx := sw.Context()
		test.That(t, ctx, test.ShouldNotBeNil)
		test.That(t, ctx.Err(), test.ShouldBeError, context.Canceled)
	})

	t.Run("one worker background constructor", func(t *testing.T) {
		sw := NewBackgroundStoppableWorkers(normalWorker)
		sw.Stop()
		ctx := sw.Context()
		test.That(t, ctx, test.ShouldNotBeNil)
		test.That(t, ctx.Err(), test.ShouldBeError, context.Canceled)
	})

	t.Run("heavy workers", func(t *testing.T) {
		sw := NewStoppableWorkers(ctx)
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
					// Remain sensitive to context in sending to `ints` channel, so this
					// goroutine does not get hung when `readWorker` exits without
					// reading the last int.
					select {
					case <-ctx.Done():
					case ints <- count:
					}
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

		sw := NewBackgroundStoppableWorkers(writeWorker, readWorker)
		// Sleep for a second to let concurrent workers do work.
		time.Sleep(500 * time.Millisecond)
		sw.Stop()

		test.That(t, len(receivedInts), test.ShouldBeGreaterThan, 0)
	})

	t.Run("nested workers", func(t *testing.T) {
		sw := NewStoppableWorkers(ctx)
		sw.Add(nestedWorkersWorker)
		sw.Stop()
	})

	t.Run("panicking worker", func(t *testing.T) {
		sw := NewStoppableWorkers(ctx)
		// Both adding and stopping a panicking worker should cause no `panic`s.
		sw.Add(panickingWorker)
		sw.Stop()
	})

	t.Run("already stopped", func(t *testing.T) {
		sw := NewStoppableWorkers(ctx)
		sw.Stop()
		sw.Add(normalWorker) // adding after Stop should cause no `panic`
		sw.Stop()            // stopping twice should cause no `panic`
	})
}

func TestStoppableWorkersWithTicker(t *testing.T) {
	timesCalled := 0
	workFn := func(ctx context.Context) {
		timesCalled++
		select {
		case <-time.After(24 * time.Hour):
			t.Log("Failed to observe `Stop` call.")
			// Realistically, the go test timeout will be hit and not this `FailNow` call.
			t.FailNow()
		case <-ctx.Done():
			return
		}
	}

	// Create a worker with a ticker << the sleep time the test will use. The work function
	// increments a counter and hangs. This test will logically assert that:
	// - The work function was called exactly once.
	// - The work function was passed a context that observed `Stop` was called.
	sw := NewStoppableWorkerWithTicker(time.Millisecond, workFn)
	time.Sleep(time.Second)
	sw.Stop()

	test.That(t, timesCalled, test.ShouldEqual, 1)
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
	nestedSW := NewStoppableWorkers(ctx)
	nestedSW.Add(normalWorker)

	normalWorker(ctx)
}

func panickingWorker(_ context.Context) {
	panic("this worker panicked; ignore expected stack trace above")
}
