package rpc

import (
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
)

func TestMemoryWebRTCCallQueue(t *testing.T) {
	testWebRTCCallQueue(t, func(t *testing.T) (WebRTCCallQueue, WebRTCCallQueue, func()) {
		t.Helper()
		logger := golog.NewTestLogger(t)
		callQueue := NewMemoryWebRTCCallQueue(logger)
		return callQueue, callQueue, func() {
			test.That(t, callQueue.Close(), test.ShouldBeNil)
		}
	})
}
