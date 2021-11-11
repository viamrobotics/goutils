package rpcwebrtc

import (
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

func TestMongoDBCallQueue(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)
	callQueue, err := NewMongoDBCallQueue(client)
	test.That(t, err, test.ShouldBeNil)

	testCallQueue(t, callQueue)
	test.That(t, callQueue.Close(), test.ShouldBeNil)
}
