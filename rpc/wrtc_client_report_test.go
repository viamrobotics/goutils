package rpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
	"google.golang.org/grpc"

	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestClassifySignalingPath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		address  string
		usingDNS bool
		expected webrtcpb.ConnectionSignalingPath
	}{
		{"mdns wins regardless of address", "app.viam.com:443", true, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_MDNS_LOCAL},
		{"app.viam.com is cloud", "app.viam.com:443", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED},
		{"app.viam.dev is cloud", "app.viam.dev", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED},
		{"scheme is stripped", "https://app.viam.com:443", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED},
		{"case insensitive", "APP.VIAM.COM:443", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED},
		{"localhost is local", "localhost:8080", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL},
		{"loopback ip is local", "127.0.0.1:9000", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL},
		{"lan ip is local", "10.1.2.3:443", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL},
		{"machine fqdn is local", "my-robot.abc123.viam.cloud:443", false, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test.That(t, classifySignalingPath(tc.address, tc.usingDNS), test.ShouldEqual, tc.expected)
		})
	}
}

func TestFurthestPendingReport(t *testing.T) {
	report := func(stage webrtcpb.DialStage) pendingReport {
		return pendingReport{req: &webrtcpb.ReportConnectionMetadataRequest{ReachedStage: stage}}
	}
	stageOf := func(r pendingReport) webrtcpb.DialStage { return r.req.GetReachedStage() }

	t.Run("a ready success beats every failure regardless of order", func(t *testing.T) {
		reports := []pendingReport{
			report(webrtcpb.DialStage_DIAL_STAGE_CONFIG_FETCHED),
			report(webrtcpb.DialStage_DIAL_STAGE_READY),
			report(webrtcpb.DialStage_DIAL_STAGE_OFFER_SENT),
		}
		test.That(t, stageOf(furthestPendingReport(reports)), test.ShouldEqual, webrtcpb.DialStage_DIAL_STAGE_READY)
	})

	t.Run("among failures the latest stage wins", func(t *testing.T) {
		reports := []pendingReport{
			report(webrtcpb.DialStage_DIAL_STAGE_SIGNALING_CONNECTED),
			report(webrtcpb.DialStage_DIAL_STAGE_ANSWER_RECEIVED),
			report(webrtcpb.DialStage_DIAL_STAGE_CONFIG_FETCHED),
		}
		test.That(t, stageOf(furthestPendingReport(reports)), test.ShouldEqual, webrtcpb.DialStage_DIAL_STAGE_ANSWER_RECEIVED)
	})

	t.Run("a single report is returned as-is", func(t *testing.T) {
		only := report(webrtcpb.DialStage_DIAL_STAGE_OFFER_SENT)
		test.That(t, stageOf(furthestPendingReport([]pendingReport{only})), test.ShouldEqual, webrtcpb.DialStage_DIAL_STAGE_OFFER_SENT)
	})
}

func TestClassifyCandidate(t *testing.T) {
	const candID = "c1"
	cand := func(ct webrtc.ICECandidateType, ip string) webrtc.StatsReport {
		return webrtc.StatsReport{candID: webrtc.ICECandidateStats{CandidateType: ct, IP: ip}}
	}

	t.Run("host", func(t *testing.T) {
		got := classifyCandidate(cand(webrtc.ICECandidateTypeHost, "1.2.3.4"), candID)
		test.That(t, got.GetType(), test.ShouldEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_HOST)
		test.That(t, got.GetRelayAddress(), test.ShouldEqual, "")
	})
	t.Run("srflx and prflx are stun", func(t *testing.T) {
		for _, ct := range []webrtc.ICECandidateType{webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypePrflx} {
			got := classifyCandidate(cand(ct, "5.6.7.8"), candID)
			test.That(t, got.GetType(), test.ShouldEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_STUN)
			test.That(t, got.GetRelayAddress(), test.ShouldEqual, "")
		}
	})
	t.Run("relay carries the relay address", func(t *testing.T) {
		got := classifyCandidate(cand(webrtc.ICECandidateTypeRelay, "34.0.0.1"), candID)
		test.That(t, got.GetType(), test.ShouldEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_RELAY)
		test.That(t, got.GetRelayAddress(), test.ShouldEqual, "34.0.0.1")
	})
	t.Run("missing candidate id is unspecified", func(t *testing.T) {
		got := classifyCandidate(cand(webrtc.ICECandidateTypeHost, "1.2.3.4"), "nope")
		test.That(t, got.GetType(), test.ShouldEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED)
	})
}

func TestClassifyConnectionNilPeer(t *testing.T) {
	local, remote := classifyConnection(nil)
	test.That(t, local.GetType(), test.ShouldEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED)
	test.That(t, remote.GetType(), test.ShouldEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED)
}

// fakeClientConn is a minimal ClientConn that records whether it was used to send an RPC and closed.
type fakeClientConn struct {
	invoked bool
	closed  bool
}

func (c *fakeClientConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	c.invoked = true
	return nil
}

func (c *fakeClientConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, nil
}
func (c *fakeClientConn) PeerConn() *webrtc.PeerConnection { return nil }
func (c *fakeClientConn) Close() error                     { c.closed = true; return nil }

func TestDialReportCollectorFlush(t *testing.T) {
	logger := golog.NewTestLogger(t)
	report := func(stage webrtcpb.DialStage, conn ClientConn) pendingReport {
		return pendingReport{req: &webrtcpb.ReportConnectionMetadataRequest{ReachedStage: stage}, conn: conn}
	}

	t.Run("successful dial sends the ready report over its own conn and closes all conns", func(t *testing.T) {
		ready, loser := &fakeClientConn{}, &fakeClientConn{}
		c := &dialReportCollector{ctx: context.Background(), host: "h", logger: logger}
		c.add(report(webrtcpb.DialStage_DIAL_STAGE_SIGNALING_CONNECTED, loser))
		c.add(report(webrtcpb.DialStage_DIAL_STAGE_READY, ready))
		c.flush(nil)
		test.That(t, ready.invoked, test.ShouldBeTrue)
		test.That(t, loser.invoked, test.ShouldBeFalse)
		test.That(t, ready.closed, test.ShouldBeTrue)
		test.That(t, loser.closed, test.ShouldBeTrue)
	})

	t.Run("successful dial with only a failure report (e.g. cached winner) sends nothing but still closes conns", func(t *testing.T) {
		loser := &fakeClientConn{}
		c := &dialReportCollector{ctx: context.Background(), host: "h", logger: logger}
		c.add(report(webrtcpb.DialStage_DIAL_STAGE_SIGNALING_CONNECTED, loser))
		c.flush(nil)
		test.That(t, loser.invoked, test.ShouldBeFalse)
		test.That(t, loser.closed, test.ShouldBeTrue)
	})

	t.Run("failed dial sends the furthest failure and closes conns", func(t *testing.T) {
		early, furthest := &fakeClientConn{}, &fakeClientConn{}
		c := &dialReportCollector{ctx: context.Background(), host: "h", logger: logger}
		c.add(report(webrtcpb.DialStage_DIAL_STAGE_SIGNALING_CONNECTED, early))
		c.add(report(webrtcpb.DialStage_DIAL_STAGE_OFFER_SENT, furthest))
		c.flush(errors.New("dial failed"))
		test.That(t, furthest.invoked, test.ShouldBeTrue)
		test.That(t, early.invoked, test.ShouldBeFalse)
		test.That(t, early.closed, test.ShouldBeTrue)
		test.That(t, furthest.closed, test.ShouldBeTrue)
	})
}

// TestReportConnectionMetadataOncePerDial dials a robot over WebRTC and asserts the dialing client
// sends exactly one connection-metadata report describing the successful dial.
func TestReportConnectionMetadataOncePerDial(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() { test.That(t, signalingCallQueue.Close(), test.ShouldBeNil) }()

	const host = "yeehaw"
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger, defaultHeartbeatInterval)
	defer signalingServer.Close()

	// Capture every report the base signaling server receives.
	reportCh := make(chan *webrtcpb.ReportConnectionMetadataRequest, 8)
	signalingServer.SetConnectionMetadataHandler(
		func(_ context.Context, _ string, req *webrtcpb.ReportConnectionMetadataRequest) {
			reportCh <- req
		})

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)
	serveDone := make(chan error)
	go func() { serveDone <- grpcServer.Serve(grpcListener) }()

	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		[]string{host},
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)
	answerer.Start()

	var ch ClientConn
	var dialErr error
	for range 3 {
		waitForAnswererOnline(context.Background(), t, []string{host}, signalingCallQueue)
		ch, dialErr = DialWebRTC(
			context.Background(),
			grpcListener.Addr().String(),
			host,
			logger,
			WithWebRTCOptions(DialWebRTCOptions{SignalingInsecure: true}),
			WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
		)
		if dialErr == nil {
			break
		}
	}
	test.That(t, dialErr, test.ShouldBeNil)

	var req *webrtcpb.ReportConnectionMetadataRequest
	select {
	case req = <-reportCh:
	case <-time.After(10 * time.Second):
		t.Fatal("expected a connection-metadata report")
	}

	test.That(t, req.GetReachedStage(), test.ShouldEqual, webrtcpb.DialStage_DIAL_STAGE_READY)
	test.That(t, req.GetSignalingPath(), test.ShouldEqual, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL)
	test.That(t, req.GetSdkType(), test.ShouldEqual, webrtcpb.SDKType_SDK_TYPE_GO)
	test.That(t, req.GetFailureCode(), test.ShouldEqual, int32(0))
	test.That(t, req.GetLocal().GetType(), test.ShouldNotEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED)
	test.That(t, req.GetRemote().GetType(), test.ShouldNotEqual, webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED)

	// Exactly one: no duplicate from a racing/cancelled sibling attempt.
	select {
	case extra := <-reportCh:
		t.Fatalf("expected exactly one report, got a second: %+v", extra)
	case <-time.After(2 * time.Second):
	}

	test.That(t, ch.Close(), test.ShouldBeNil)
	answerer.Stop()
	grpcServer.Stop()
	webrtcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}
