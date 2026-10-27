package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// callServerStub satisfies webrtcpb.SignalingService_CallServer with only what Call touches.
// SetHeader, SendHeader, SetTrailer, SendMsg, RecvMsg are never called by the Call handler;
// the embedded nil ServerStream makes any accidental use crash loudly rather than succeed
// silently.
type callServerStub struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*webrtcpb.CallResponse
}

func (s *callServerStub) Context() context.Context            { return s.ctx }
func (s *callServerStub) Send(r *webrtcpb.CallResponse) error { s.sent = append(s.sent, r); return nil }

func TestCallRefusesUnauthCallerWhenAnswererCannotAuth(t *testing.T) {
	logger := golog.NewTestLogger(t)
	queue := NewMemoryWebRTCCallQueue(logger)
	defer func() { test.That(t, queue.Close(), test.ShouldBeNil) }()

	srv := NewWebRTCSignalingServer(queue, nil, logger, time.Second)
	defer srv.Close()

	const host = "robot-host"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Mark the caller as having reached the public method without auth, so Call will set
	// mustAuthCaller=true. The host comes in on metadata like a real caller would send it.
	callerCtx := contextWithUnauthenticatedCaller(ctx)
	callerCtx = metadata.NewIncomingContext(callerCtx,
		metadata.Pairs(RPCHostMetadataField, host))
	stub := &callServerStub{ctx: callerCtx}

	// Stand in for the answerer: receive the offer and reply with CanAuthCallers=false.
	answererDone := make(chan error, 1)
	go func() {
		exchange, err := queue.RecvOffer(ctx, []string{host})
		if err != nil {
			answererDone <- err
			return
		}
		sdp := "answerer-sdp"
		answererDone <- exchange.AnswererRespond(ctx, WebRTCCallAnswer{
			InitialSDP:     &sdp,
			CanAuthCallers: false,
		})
	}()

	err := srv.Call(&webrtcpb.CallRequest{Sdp: "caller-sdp", DisableTrickle: true}, stub)
	test.That(t, status.Code(err), test.ShouldEqual, codes.Unauthenticated)
	// The caller must not have seen a CallResponse_Init, since the degraded path was refused.
	test.That(t, stub.sent, test.ShouldBeEmpty)

	// Drain the answerer goroutine so leaks are visible if the queue stops delivering.
	select {
	case <-answererDone:
	case <-time.After(5 * time.Second):
		t.Fatal("answerer goroutine did not exit")
	}
}

func TestCallAllowsUnauthCallerWhenAnswererCanAuth(t *testing.T) {
	logger := golog.NewTestLogger(t)
	queue := NewMemoryWebRTCCallQueue(logger)
	defer func() { test.That(t, queue.Close(), test.ShouldBeNil) }()

	srv := NewWebRTCSignalingServer(queue, nil, logger, time.Second)
	defer srv.Close()

	const host = "robot-host"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	callerCtx := contextWithUnauthenticatedCaller(ctx)
	callerCtx = metadata.NewIncomingContext(callerCtx,
		metadata.Pairs(RPCHostMetadataField, host))
	callerCtx, callerCancel := context.WithCancel(callerCtx)
	defer callerCancel()
	stub := &callServerStub{ctx: callerCtx}

	go func() {
		exchange, err := queue.RecvOffer(ctx, []string{host})
		if err != nil {
			return
		}
		sdp := "answerer-sdp"
		_ = exchange.AnswererRespond(ctx, WebRTCCallAnswer{
			InitialSDP:     &sdp,
			CanAuthCallers: true,
		})
	}()

	// The handler blocks waiting for the answerer's Done; cancel once we've seen the Init.
	done := make(chan error, 1)
	go func() {
		done <- srv.Call(&webrtcpb.CallRequest{Sdp: "caller-sdp", DisableTrickle: true}, stub)
	}()

	// Poll briefly for the Init to be forwarded, then cancel the caller to end Call.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(stub.sent) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	callerCancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Call did not return after caller cancel")
	}

	test.That(t, len(stub.sent), test.ShouldBeGreaterThanOrEqualTo, 1)
	init := stub.sent[0].GetInit()
	test.That(t, init, test.ShouldNotBeNil)
	test.That(t, init.GetMustAuthAgainstAnswerer(), test.ShouldBeTrue)
}
