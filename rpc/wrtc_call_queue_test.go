package rpc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"
)

func testWebRTCCallQueue(t *testing.T, setupQueues func(t *testing.T) (WebRTCCallQueue, WebRTCCallQueue, func())) {
	t.Run("sending an offer for too long should signal done", func(t *testing.T) {
		callerQueue, _, teardown := setupQueues(t)
		defer teardown()

		undo := setDefaultOfferDeadline(time.Second)
		defer undo()

		host := primitive.NewObjectID().Hex()
		_, _, ansCtx, _, err := callerQueue.SendOfferInit(context.Background(), host, "somesdp", false)
		test.That(t, err, test.ShouldBeNil)
		<-ansCtx
	})

	t.Run("recv can get caller updates and done", func(t *testing.T) {
		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		host := primitive.NewObjectID().Hex()
		recvErrCh := make(chan error)
		recvCandCh := make(chan webrtc.ICECandidateInit)
		sdpMid := "sdpmid"
		sdpMLineIndex := uint16(1)
		usernameFragment := "ufrag"
		c1 := &webrtc.ICECandidateInit{
			Candidate:        "c1",
			SDPMid:           &sdpMid,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: &usernameFragment,
		}
		c2 := &webrtc.ICECandidateInit{
			Candidate:        "c2",
			SDPMid:           &sdpMid,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: &usernameFragment,
		}
		c3 := &webrtc.ICECandidateInit{
			Candidate:        "c3",
			SDPMid:           &sdpMid,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: &usernameFragment,
		}
		done := make(chan struct{})
		defer func() { <-done }()
		go func() {
			offer, err := answererQueue.RecvOffer(context.Background(), []string{host})
			if err != nil {
				recvErrCh <- err
				return
			}

			sdp := "world"
			recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{InitialSDP: &sdp})
			recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{Candidate: c1})
			recvErrCh <- offer.AnswererDone(context.Background())
			recvCandCh <- <-offer.CallerCandidates()
			recvCandCh <- <-offer.CallerCandidates()
			<-offer.CallerDone()
			close(done)
		}()

		newUUID, answers, answersDone, cancel, err := callerQueue.SendOfferInit(context.Background(), host, "hello", false)
		defer cancel()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, newUUID, test.ShouldNotBeEmpty)
		ans := <-answers
		test.That(t, ans.InitialSDP, test.ShouldNotBeNil)
		test.That(t, *ans.InitialSDP, test.ShouldEqual, "world")
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		ans = <-answers
		test.That(t, ans.Candidate, test.ShouldNotBeNil)
		test.That(t, ans.Candidate, test.ShouldResemble, c1)
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		<-answersDone
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		test.That(t, callerQueue.SendOfferUpdate(context.Background(), host, newUUID, *c2), test.ShouldBeNil)
		test.That(t, <-recvCandCh, test.ShouldResemble, *c2)
		test.That(t, callerQueue.SendOfferUpdate(context.Background(), host, newUUID, *c3), test.ShouldBeNil)
		test.That(t, <-recvCandCh, test.ShouldResemble, *c3)
		test.That(t, callerQueue.SendOfferDone(context.Background(), host, newUUID), test.ShouldBeNil)
	})

	t.Run("recv can get caller updates and error", func(t *testing.T) {
		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		host := primitive.NewObjectID().Hex()
		recvErrCh := make(chan error)
		recvCandCh := make(chan webrtc.ICECandidateInit)
		sdpMid := "sdpmid"
		sdpMLineIndex := uint16(1)
		usernameFragment := "ufrag"
		c1 := &webrtc.ICECandidateInit{
			Candidate:        "c1",
			SDPMid:           &sdpMid,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: &usernameFragment,
		}
		c2 := &webrtc.ICECandidateInit{
			Candidate:        "c2",
			SDPMid:           &sdpMid,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: &usernameFragment,
		}
		c3 := &webrtc.ICECandidateInit{
			Candidate:        "c3",
			SDPMid:           &sdpMid,
			SDPMLineIndex:    &sdpMLineIndex,
			UsernameFragment: &usernameFragment,
		}
		done := make(chan struct{})
		defer func() { <-done }()
		go func() {
			offer, err := answererQueue.RecvOffer(context.Background(), []string{host})
			if err != nil {
				recvErrCh <- err
				return
			}

			sdp := "world"
			recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{InitialSDP: &sdp})
			recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{Candidate: c1})
			recvErrCh <- offer.AnswererDone(context.Background())
			recvCandCh <- <-offer.CallerCandidates()
			recvCandCh <- <-offer.CallerCandidates()
			<-offer.CallerDone()
			recvErrCh <- offer.CallerErr()
			close(done)
		}()

		newUUID, answers, answersDone, cancel, err := callerQueue.SendOfferInit(context.Background(), host, "hello", false)
		defer cancel()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, newUUID, test.ShouldNotBeEmpty)
		ans := <-answers
		test.That(t, ans.InitialSDP, test.ShouldNotBeNil)
		test.That(t, *ans.InitialSDP, test.ShouldEqual, "world")
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		ans = <-answers
		test.That(t, ans.Candidate, test.ShouldNotBeNil)
		test.That(t, ans.Candidate, test.ShouldResemble, c1)
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		<-answersDone
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		test.That(t, callerQueue.SendOfferUpdate(context.Background(), host, newUUID, *c2), test.ShouldBeNil)
		test.That(t, <-recvCandCh, test.ShouldResemble, *c2)
		test.That(t, callerQueue.SendOfferUpdate(context.Background(), host, newUUID, *c3), test.ShouldBeNil)
		test.That(t, <-recvCandCh, test.ShouldResemble, *c3)
		test.That(t, callerQueue.SendOfferError(context.Background(), host, newUUID, errors.New("whoops")), test.ShouldBeNil)
		test.That(t, <-recvErrCh, test.ShouldBeError, errors.New("whoops"))
	})

	t.Run("canceling an offer should eventually close answerer responses", func(t *testing.T) {
		callerQueue, _, teardown := setupQueues(t)
		defer teardown()

		host := primitive.NewObjectID().Hex()
		newUUID, _, answersDone, cancel, err := callerQueue.SendOfferInit(context.Background(), host, "hello", false)
		cancel()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, newUUID, test.ShouldNotBeEmpty)
		<-answersDone
	})

	t.Run("sending successfully with an sdp", func(t *testing.T) {
		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		hosts := []string{primitive.NewObjectID().Hex(), primitive.NewObjectID().Hex()}
		for idx, host := range hosts {
			t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
				recvErrCh := make(chan error)
				sdpMid := "sdpmid"
				sdpMLineIndex := uint16(1)
				usernameFragment := "ufrag"
				c1 := &webrtc.ICECandidateInit{
					Candidate:        "c1",
					SDPMid:           &sdpMid,
					SDPMLineIndex:    &sdpMLineIndex,
					UsernameFragment: &usernameFragment,
				}
				done := make(chan struct{})
				defer func() { <-done }()
				go func() {
					offer, err := answererQueue.RecvOffer(context.Background(), hosts)
					if err != nil {
						recvErrCh <- err
						return
					}

					sdp := "world"
					recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{InitialSDP: &sdp})
					recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{Candidate: c1})
					recvErrCh <- offer.AnswererDone(context.Background())
					close(done)
				}()

				newUUID, answers, answersDone, cancel, err := callerQueue.SendOfferInit(context.Background(), host, "hello", false)
				defer cancel()
				test.That(t, err, test.ShouldBeNil)
				test.That(t, newUUID, test.ShouldNotBeEmpty)
				ans := <-answers
				test.That(t, ans.InitialSDP, test.ShouldNotBeNil)
				test.That(t, *ans.InitialSDP, test.ShouldEqual, "world")
				test.That(t, <-recvErrCh, test.ShouldBeNil)
				ans = <-answers
				test.That(t, ans.Candidate, test.ShouldNotBeNil)
				test.That(t, ans.Candidate, test.ShouldResemble, c1)
				test.That(t, <-recvErrCh, test.ShouldBeNil)
				<-answersDone
				test.That(t, <-recvErrCh, test.ShouldBeNil)
			})
		}
	})

	t.Run("sending successfully with an error", func(t *testing.T) {
		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		host := primitive.NewObjectID().Hex()
		recvErrCh := make(chan error)
		done := make(chan struct{})
		defer func() { <-done }()
		go func() {
			offer, err := answererQueue.RecvOffer(context.Background(), []string{host})
			if err != nil {
				recvErrCh <- err
				return
			}

			recvErrCh <- offer.AnswererRespond(context.Background(), WebRTCCallAnswer{Err: errors.New("whoops")})
			recvErrCh <- offer.AnswererDone(context.Background())
			close(done)
		}()

		newUUID, answers, answersDone, cancel, err := callerQueue.SendOfferInit(context.Background(), host, "hello", false)
		defer cancel()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, newUUID, test.ShouldNotBeEmpty)
		test.That(t, (<-answers).Err.Error(), test.ShouldContainSubstring, "whoops")
		test.That(t, <-recvErrCh, test.ShouldBeNil)
		<-answersDone
		test.That(t, <-recvErrCh, test.ShouldBeNil)
	})

	t.Run("receiving from a host not sent to should not work", func(t *testing.T) {
		callerQueue, answererQueue, teardown := setupQueues(t)
		defer teardown()

		undo := setDefaultOfferDeadline(10 * time.Second)
		defer undo()

		recvErrCh := make(chan error)
		done := make(chan struct{})
		defer func() { <-done }()
		go func() {
			// should be ample time in tests
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := answererQueue.RecvOffer(ctx, []string{primitive.NewObjectID().Hex()})
			recvErrCh <- err
			close(done)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, ansCtx, _, err := callerQueue.SendOfferInit(ctx, primitive.NewObjectID().Hex(), "hello", false)
		test.That(t, err, test.ShouldBeNil)
		<-ansCtx
		recvErr := <-recvErrCh
		test.That(t, recvErr, test.ShouldNotBeNil)
		test.That(t, recvErr, test.ShouldWrap, context.DeadlineExceeded)
	})
}
