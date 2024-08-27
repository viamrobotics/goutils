package utils_test

import (
	"bytes"
	"context"
	"testing"
	"time"

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
	})

	t.Run("one worker background constructor", func(t *testing.T) {
		sw := utils.NewBackgroundStoppableWorkers(normalWorker)
		sw.Stop()
	})

	t.Run("heavy workers", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		sw.Add(heavyWorker)
		sw.Add(heavyWorker)
		sw.Add(heavyWorker)

		// Sleep for a second to let heavy workers do work.
		time.Sleep(1 * time.Second)
		sw.Stop()
	})

	t.Run("concurrent workers", func(t *testing.T) {
		sw := utils.NewStoppableWorkers(ctx)
		concurrentWorker := func(ctx context.Context) {
			go normalWorker(ctx)
		}
		sw.Add(concurrentWorker)
		sw.Stop()
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
