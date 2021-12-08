package rpc

import (
	"testing"

	"go.viam.com/test"
)

func TestMemoryWebRTCCallQueue(t *testing.T) {
	callQueue := NewMemoryWebRTCCallQueue()
	testWebRTCCallQueue(t, callQueue)
	test.That(t, callQueue.Close(), test.ShouldBeNil)
}
