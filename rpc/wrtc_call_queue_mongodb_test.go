package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

func TestMongoDBWebRTCCallQueue(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)

	testWebRTCCallQueue(t, func(t *testing.T) (WebRTCCallQueue, WebRTCCallQueue, func()) {
		t.Helper()
		logger := golog.NewTestLogger(t)
		callQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger)
		test.That(t, err, test.ShouldBeNil)
		return callQueue, callQueue, func() {
			test.That(t, callQueue.Close(), test.ShouldBeNil)
		}
	})
}

func TestMongoDBWebRTCCallQueueMulti(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)

	const maxCallerQueueSize = 3
	setupQueues := func(t *testing.T) (WebRTCCallQueue, WebRTCCallQueue, func()) {
		t.Helper()
		logger := golog.NewTestLogger(t)
		callerQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString()+"-caller", maxCallerQueueSize, client, logger)
		test.That(t, err, test.ShouldBeNil)

		answererQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString()+"-answerer", maxCallerQueueSize, client, logger)
		test.That(t, err, test.ShouldBeNil)
		return callerQueue, answererQueue, func() {
			test.That(t, callerQueue.Close(), test.ShouldBeNil)
			test.That(t, answererQueue.Close(), test.ShouldBeNil)
		}
	}

	testWebRTCCallQueue(t, setupQueues)

	t.Run("max queue size", func(t *testing.T) {
		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		host := primitive.NewObjectID().Hex()

		type offerState struct {
			Done   <-chan struct{}
			Cancel func()
		}
		offers := make([]offerState, 0, maxCallerQueueSize)
		defer func() {
			for _, offer := range offers {
				offer.Cancel()
				<-offer.Done
			}
		}()

		t.Logf("start up %d callers (the max)", maxCallerQueueSize)
		for i := 0; i < maxCallerQueueSize; i++ {
			_, _, respDone, cancel, err := callerQueue.SendOfferInit(ctx, host, "somesdp", false)
			test.That(t, err, test.ShouldBeNil)
			offers = append(offers, offerState{Done: respDone, Cancel: cancel})
			time.Sleep(2 * time.Second)
		}

		t.Logf("the next caller should fail from either queue")
		_, _, _, _, err := callerQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldResemble, errTooManyConns)
		_, _, _, _, err = answererQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldResemble, errTooManyConns)

		t.Logf("but canceling one should allow the next")
		offers[0].Cancel()
		<-offers[0].Done

		time.Sleep(2 * time.Second)

		_, _, respDone, cancel, err := callerQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldBeNil)
		offers[0] = offerState{Done: respDone, Cancel: cancel}

		exchanges := make([]WebRTCCallOfferExchange, 0, maxHostAnswerersSize)
		defer func() {
			for _, exchange := range exchanges {
				test.That(t, exchange.AnswererDone(ctx), test.ShouldBeNil)
				<-exchange.CallerDone()
			}
		}()

		t.Logf("start up %d answerers (the max)", maxHostAnswerersSize)
		for i := 0; i < maxHostAnswerersSize; i++ {
			exchange, err := answererQueue.RecvOffer(ctx, []string{host})
			test.That(t, err, test.ShouldBeNil)
			exchanges = append(exchanges, exchange)
		}

		time.Sleep(2 * time.Second)

		t.Logf("the next answerer should fail from either queue")
		_, err = answererQueue.RecvOffer(ctx, []string{host})
		test.That(t, err, test.ShouldResemble, errTooManyConns)
		_, err = callerQueue.RecvOffer(ctx, []string{host})
		test.That(t, err, test.ShouldResemble, errTooManyConns)

		t.Logf("but canceling one should allow the next")
		test.That(t, exchanges[0].AnswererDone(ctx), test.ShouldBeNil)
		<-exchanges[0].CallerDone()

		time.Sleep(2 * time.Second)

		exchange, err := answererQueue.RecvOffer(ctx, []string{host})
		test.That(t, err, test.ShouldBeNil)
		exchanges[0] = exchange
	})
}
