package rpc

import (
	"context"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

func TestMongoDBWebRTCCallQueue(t *testing.T) {
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	callQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), client, logger)
	test.That(t, err, test.ShouldBeNil)

	testWebRTCCallQueue(t, callQueue)
	test.That(t, callQueue.Close(), test.ShouldBeNil)
}
