package rpcwebrtc

import (
	"testing"

	"go.viam.com/test"
)

func TestMemoryCallQueue(t *testing.T) {
	callQueue := NewMemoryCallQueue()
	testCallQueue(t, callQueue)
	test.That(t, callQueue.Close(), test.ShouldBeNil)
}
