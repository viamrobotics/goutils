package rpc

import (
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
)

func TestMemoryWebRTCCallQueue(t *testing.T) {
	logger := golog.NewTestLogger(t)
	callQueue := NewMemoryWebRTCCallQueue(logger)
	testWebRTCCallQueue(t, callQueue)
	test.That(t, callQueue.Close(), test.ShouldBeNil)
}
