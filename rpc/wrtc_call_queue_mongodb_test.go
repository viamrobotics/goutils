package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.viam.com/test"

	"go.viam.com/utils/testutils"
)

var operatorID = uuid.NewString()

func TestMongoDBWebRTCCallQueue(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)

	testWebRTCCallQueue(t, func(t *testing.T) (WebRTCCallQueue, WebRTCCallQueue, func()) {
		t.Helper()
		test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
		logger := golog.NewTestLogger(t)
		callQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), operatorID, 50, client, logger,
			func(hosts []string, atTime time.Time) {}, nil)
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
		callerQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString()+"-caller",
			maxCallerQueueSize, client, logger, func(hosts []string, atTime time.Time) {}, nil)
		test.That(t, err, test.ShouldBeNil)

		answererQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString()+"-answerer",
			maxCallerQueueSize, client, logger, func(hosts []string, atTime time.Time) {}, nil)
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

		// We need to mock an answerer being online for this host to be able to get offers through otherwise Call attempts will fail due to no
		// answerers being online
		addFakeAnswererForHost(t, client, host)
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

		// Now that we are starting up real answerers, we can remove the fake one
		removeOperatorDocument(t, client, operatorID)
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

	t.Run("ActiveAnswerer", func(t *testing.T) {
		client := testutils.BackingMongoDBClient(t)
		activeAnswererChannelStub := make(chan int, 3)
		defer close(activeAnswererChannelStub)

		logger := golog.NewTestLogger(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
		answererQueue, err := NewMongoDBWebRTCCallQueue(
			ctx, uuid.NewString()+"-answerer",
			1, client, logger, func(hostnames []string, atTime time.Time) { activeAnswererChannelStub <- len(hostnames) }, nil)
		test.That(t, err, test.ShouldBeNil)
		defer answererQueue.Close()

		host1 := primitive.NewObjectID().Hex()
		host2 := primitive.NewObjectID().Hex()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// this is mimicking the caller - this sets the hostnames into the hosts array in the operatorLivenessLoop
			_, _ = answererQueue.RecvOffer(ctx, []string{host1, host2})
		}()

		// val is zero when no hostnames are online, so repeat until two hostnames are returned.
		testutils.WaitForAssertion(t, func(tb testing.TB) {
			tb.Helper()
			val, ok := <-activeAnswererChannelStub
			test.That(tb, ok, test.ShouldBeTrue)
			test.That(tb, val, test.ShouldEqual, 2)
		})

		cancel()
		wg.Wait()
	})
}

func TestMongoDBWebRTCCallQueueOperatorDocDeleted(t *testing.T) {
	logger := golog.NewTestLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(ctx), test.ShouldBeNil)
	opID := uuid.NewString() + "-answerer"
	answererQueue, err := NewMongoDBWebRTCCallQueue(ctx, opID, 1, client, logger, nil, nil)
	test.That(t, err, test.ShouldBeNil)
	defer answererQueue.Close()

	host := primitive.NewObjectID().Hex()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// this is mimicking the caller - this sets the hostname into the hosts array in the operatorLivenessLoop
		_, _ = answererQueue.RecvOffer(ctx, []string{host})
	}()

	waitForHostOnOperator := func(t *testing.T) {
		testutils.WaitForAssertion(t, func(tb testing.TB) {
			tb.Helper()
			filter := bson.M{
				webrtcOperatorHostsHostCombinedField:         host,
				webrtcOperatorHostsAnswererSizeCombinedField: bson.M{"$eq": 1},
			}

			count, err := client.Database(mongodbWebRTCCallQueueDBName).
				Collection(mongodbWebRTCCallQueueOperatorsCollName).
				CountDocuments(ctx, filter)
			test.That(tb, err, test.ShouldBeNil)
			test.That(tb, count, test.ShouldEqual, 1)
		})
	}

	// This test waits for the host to appear on the operator document.
	// It then:
	//   1. Deletes the operator document (simulating a document expiration caused
	//      by network or any other issues)
	//   2. Asserts that the operator document is re-inserted into the operators collection
	//      and the host is also on that operator document.
	waitForHostOnOperator(t)
	removeOperatorDocument(t, client, opID)
	waitForHostOnOperator(t)

	cancel()
	wg.Wait()
}

func addFakeAnswererForHost(t *testing.T, client *mongo.Client, host string) {
	t.Helper()
	_, err := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCCallQueueOperatorsCollName).InsertOne(context.Background(),
		bson.M{
			"_id":       operatorID,
			"expire_at": time.Now().Add(time.Hour),
			"hosts": []bson.M{{
				"host":          host,
				"caller_size":   int64(0),
				"answerer_size": int64(1),
			}},
		})
	test.That(t, err, test.ShouldBeNil)
}

// removeOperatorDocument will remove the specified operator document from the operators collection, which will
// may include fake answerers on that document.
func removeOperatorDocument(t *testing.T, client *mongo.Client, operatorID string) {
	t.Helper()
	_, err := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCCallQueueOperatorsCollName).DeleteOne(context.Background(),
		bson.M{"_id": operatorID})
	test.That(t, err, test.ShouldBeNil)
}
