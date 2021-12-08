package rpc

import (
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

func TestMongoDBWebRTCCallQueue(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)
	callQueue, err := NewMongoDBWebRTCCallQueue(client)
	test.That(t, err, test.ShouldBeNil)

	testWebRTCCallQueue(t, callQueue)
	test.That(t, callQueue.Close(), test.ShouldBeNil)
}
