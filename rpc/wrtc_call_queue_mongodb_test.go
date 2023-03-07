package rpc

import (
	"context"
	"errors"
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
		test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
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

	// we will use this to be able to have enough callers matched to answerers
	const maxCallerQueueSize = (maxHostAnswerersSize * 2)
	setupQueues := func(t *testing.T) (WebRTCCallQueue, WebRTCCallQueue, func()) {
		t.Helper()
		test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
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
		undo := setDefaultOfferDeadline(time.Minute)
		defer undo()

		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		host := primitive.NewObjectID().Hex()

		// see comment in NewMongoDBWebRTCCallQueue for hostAnswererQueueSizeMatchAggStage
		exchanges := make([]WebRTCCallOfferExchange, 0, maxHostAnswerersSize*2)
		defer func() {
			for _, exchange := range exchanges {
				test.That(t, exchange.AnswererDone(ctx), test.ShouldBeNil)
				<-exchange.CallerDone()
			}
		}()

		type offerState struct {
			CallID string
			Done   <-chan struct{}
			Cancel func()
		}
		offers := make([]offerState, 0, maxCallerQueueSize)
		defer func() {
			for _, offer := range offers {
				offer.Cancel()
				<-offer.Done
				test.That(t, callerQueue.SendOfferError(ctx, host, offer.CallID, errors.New("whoops")), test.ShouldBeNil)
			}
		}()

		t.Logf("start up %d callers (the max)", maxCallerQueueSize)
		for i := 0; i < maxCallerQueueSize; i++ {
			callID, _, respDone, cancel, err := callerQueue.SendOfferInit(ctx, host, "somesdp", false)
			test.That(t, err, test.ShouldBeNil)
			t.Logf("sent offer %d=%s", i, callID)
			offers = append(offers, offerState{CallID: callID, Done: respDone, Cancel: cancel})
			time.Sleep(2 * time.Second)
		}

		t.Log("the next caller should fail from either queue")
		_, _, _, _, err := callerQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldResemble, errTooManyConns)
		_, _, _, _, err = answererQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldResemble, errTooManyConns)

		t.Logf("but canceling one (%s) should allow the next", offers[0].CallID)
		offers[0].Cancel()
		<-offers[0].Done
		test.That(t, callerQueue.SendOfferError(ctx, host, offers[0].CallID, errors.New("whoops")), test.ShouldBeNil)

		time.Sleep(2 * time.Second)

		callID, _, respDone, cancel, err := callerQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldBeNil)
		t.Logf("sent offer %d=%s", maxCallerQueueSize, callID)
		offers[0] = offerState{CallID: callID, Done: respDone, Cancel: cancel}

		t.Logf("start up %d answerers (the max)", maxHostAnswerersSize*2)
		for i := 0; i < maxHostAnswerersSize*2; i++ {
			exchange, err := answererQueue.RecvOffer(ctx, []string{host})
			t.Logf("received offer %d=%s", i, exchange.UUID())
			test.That(t, err, test.ShouldBeNil)
			exchanges = append(exchanges, exchange)
		}

		time.Sleep(2 * time.Second)

		t.Log("the next answerer should fail from either queue")
		_, err = answererQueue.RecvOffer(ctx, []string{host})
		test.That(t, err, test.ShouldResemble, errTooManyConns)
		_, err = callerQueue.RecvOffer(ctx, []string{host})
		test.That(t, err, test.ShouldResemble, errTooManyConns)

		t.Logf("but canceling one (%s) should allow the next", exchanges[0].UUID())
		test.That(t, callerQueue.SendOfferError(ctx, host, exchanges[0].UUID(), errors.New("whoops")), test.ShouldBeNil)
		<-exchanges[0].CallerDone()
		capOfferIdx := -1
		for offerIdx, offer := range offers {
			if offer.CallID == exchanges[0].UUID() {
				capOfferIdx = offerIdx
				offer.Cancel()
				<-offer.Done
			}
		}
		test.That(t, capOfferIdx, test.ShouldNotEqual, -1)

		time.Sleep(2 * time.Second)

		callID, _, respDone, cancel, err = callerQueue.SendOfferInit(ctx, host, "somesdp", false)
		test.That(t, err, test.ShouldBeNil)
		t.Logf("sent offer %d=%s", maxCallerQueueSize+1, callID)
		offers[capOfferIdx] = offerState{CallID: callID, Done: respDone, Cancel: cancel}

		exchange, err := answererQueue.RecvOffer(ctx, []string{host})
		test.That(t, err, test.ShouldBeNil)
		t.Logf("received offer %d=%s", (maxHostAnswerersSize*2)+1, exchange.UUID())
		exchanges[0] = exchange
	})
}
