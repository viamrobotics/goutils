package rpcwebrtc

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"
)

func testCallQueue(t *testing.T, callQueue CallQueue) {
	t.Run("sending an offer for too long should fail", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := callQueue.SendOffer(ctx, primitive.NewObjectID().Hex(), "somesdp")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err, test.ShouldWrap, context.DeadlineExceeded)
	})

	t.Run("receiving an offer for too long should fail", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := callQueue.RecvOffer(ctx, primitive.NewObjectID().Hex())
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err, test.ShouldWrap, context.DeadlineExceeded)
	})

	t.Run("sending successfully with an sdp", func(t *testing.T) {
		host := primitive.NewObjectID().Hex()
		recvErrCh := make(chan error)
		go func() {
			offer, err := callQueue.RecvOffer(context.Background(), host)
			if err != nil {
				recvErrCh <- err
				return
			}

			recvErrCh <- offer.Respond(context.Background(), CallAnswer{SDP: "world"})
		}()

		resp, err := callQueue.SendOffer(context.Background(), host, "hello")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, resp, test.ShouldEqual, "world")
		test.That(t, <-recvErrCh, test.ShouldBeNil)
	})

	t.Run("sending successfully with an error", func(t *testing.T) {
		host := primitive.NewObjectID().Hex()
		recvErrCh := make(chan error)
		go func() {
			offer, err := callQueue.RecvOffer(context.Background(), host)
			if err != nil {
				recvErrCh <- err
				return
			}

			recvErrCh <- offer.Respond(context.Background(), CallAnswer{Err: errors.New("whoops")})
		}()

		_, err := callQueue.SendOffer(context.Background(), host, "hello")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "whoops")
		test.That(t, <-recvErrCh, test.ShouldBeNil)
	})

	t.Run("receiving from a host not sent to should not work", func(t *testing.T) {
		recvErrCh := make(chan error)
		go func() {
			// should be ample time in tests
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := callQueue.RecvOffer(ctx, primitive.NewObjectID().Hex())
			recvErrCh <- err
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := callQueue.SendOffer(ctx, primitive.NewObjectID().Hex(), "hello")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err, test.ShouldWrap, context.DeadlineExceeded)
		recvErr := <-recvErrCh
		test.That(t, recvErr, test.ShouldNotBeNil)
		test.That(t, recvErr, test.ShouldWrap, context.DeadlineExceeded)
	})
}
