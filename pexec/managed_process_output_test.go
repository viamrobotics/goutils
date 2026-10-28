package pexec

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

// heldWriter records everything written to it, holding up the first write until its
// release channel is closed.
type heldWriter struct {
	release chan struct{}
	hold    sync.Once

	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *heldWriter) Write(b []byte) (int, error) {
	w.hold.Do(func() { <-w.release })
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(b)
}

func (w *heldWriter) lineCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Count(w.buf.String(), "\n")
}

func TestManagedProcessOutputAtExit(t *testing.T) {
	logger := golog.NewTestLogger(t)

	const numLines = 100
	output := &heldWriter{release: make(chan struct{})}
	proc := NewManagedProcess(ProcessConfig{
		Name:      "bash",
		Args:      []string{"-c", fmt.Sprintf("for ((i=0;i<%d;i++)); do echo $i; done", numLines)},
		LogWriter: output,
		// Keep the exited process from being restarted underneath the assertions below.
		OnUnexpectedExit: func(context.Context, int) bool { return false },
	}, logger)
	test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

	// Let the process exit and be reaped while all but its first line of output is still
	// buffered in the pipe. None of that output may be lost.
	testutils.WaitForAssertion(t, func(tb testing.TB) {
		test.That(tb, proc.Status(), test.ShouldNotBeNil)
	})
	close(output.release)

	<-proc.(*managedProcess).managingCh
	test.That(t, output.lineCount(), test.ShouldEqual, numLines)
	test.That(t, proc.Stop(), test.ShouldBeNil)
}
