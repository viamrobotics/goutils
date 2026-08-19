package golog

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/atomic"
)

func TestGlobalsConcurrentUse(t *testing.T) {
	var (
		stop atomic.Bool
		wg   sync.WaitGroup
	)

	wg.Add(200)
	for i := 0; i < 100; i++ {
		go func() {
			for !stop.Load() {
				ReplaceGloabl(NewDevelopmentLogger("test"))
			}
			wg.Done()
		}()
		go func() {
			for !stop.Load() {
				Global().Info("log info")
			}
			wg.Done()
		}()
	}

	time.Sleep(100 * time.Millisecond)
	stop.Toggle()
	wg.Wait()
}
