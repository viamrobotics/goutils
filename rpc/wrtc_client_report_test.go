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

func TestSelectReport(t *testing.T) {
	req := func(stage webrtcpb.DialStage) *webrtcpb.ReportConnectionMetadataRequest {
		return &webrtcpb.ReportConnectionMetadataRequest{ReachedStage: stage}
	}

	t.Run("no reports is nothing to send", func(t *testing.T) {
		test.That(t, selectReport(nil, nil), test.ShouldBeNil)
	})

	t.Run("successful dial sends the ready report regardless of order", func(t *testing.T) {
		best := selectReport([]*webrtcpb.ReportConnectionMetadataRequest{
			req(webrtcpb.DialStage_DIAL_STAGE_CONFIG_FETCHED),
			req(webrtcpb.DialStage_DIAL_STAGE_READY),
			req(webrtcpb.DialStage_DIAL_STAGE_OFFER_SENT),
		}, nil)
		test.That(t, best.GetReachedStage(), test.ShouldEqual, webrtcpb.DialStage_DIAL_STAGE_READY)
	})

	t.Run("successful dial with only a failure report (e.g. cached winner) sends nothing", func(t *testing.T) {
		best := selectReport([]*webrtcpb.ReportConnectionMetadataRequest{
			req(webrtcpb.DialStage_DIAL_STAGE_SIGNALING_CONNECTED),
		}, nil)
		test.That(t, best, test.ShouldBeNil)
	})

	t.Run("failed dial sends the furthest failure", func(t *testing.T) {
		best := selectReport([]*webrtcpb.ReportConnectionMetadataRequest{
			req(webrtcpb.DialStage_DIAL_STAGE_SIGNALING_CONNECTED),
			req(webrtcpb.DialStage_DIAL_STAGE_ANSWER_RECEIVED),
			req(webrtcpb.DialStage_DIAL_STAGE_CONFIG_FETCHED),
		}, errors.New("dial failed"))
		test.That(t, best.GetReachedStage(), test.ShouldEqual, webrtcpb.DialStage_DIAL_STAGE_ANSWER_RECEIVED)
	})
}

func TestSetAppDialOpts(t *testing.T) {
	withCreds := func(signalingAddr string) dialOptions {
		return dialOptions{webrtcOpts: DialWebRTCOptions{
			SignalingServerAddress: signalingAddr,
			SignalingCreds:         Credentials{Type: CredentialsTypeAPIKey},
		}}
	}

	t.Run("local dial with creds is redirected to prod app over TLS", func(t *testing.T) {
		got := fixUpReportDialOpts(withCreds("192.168.1.5:8080"))
		test.That(t, got, test.ShouldNotBeNil)
		test.That(t, got.webrtcOpts.SignalingServerAddress, test.ShouldEqual, "app.viam.com:443")
		test.That(t, got.webrtcOpts.SignalingInsecure, test.ShouldBeFalse)
	})

	t.Run("cloud-signaled dial reports there unchanged", func(t *testing.T) {
		got := fixUpReportDialOpts(withCreds("app.viam.dev:443"))
		test.That(t, got, test.ShouldNotBeNil)
		test.That(t, got.webrtcOpts.SignalingServerAddress, test.ShouldEqual, "app.viam.dev:443")
	})

	t.Run("credential-less local dial is not recorded", func(t *testing.T) {
		got := fixUpReportDialOpts(dialOptions{webrtcOpts: DialWebRTCOptions{SignalingServerAddress: "192.168.1.5:8080"}})
		test.That(t, got, test.ShouldBeNil)
	})
}

// reportCapturingSignalingServer wraps a base signaling server with a ReportConnectionMetadata
// implementation that captures reports, standing in for the app's signaling server (the base server
// leaves the RPC Unimplemented).
type reportCapturingSignalingServer struct {
	*WebRTCSignalingServer
	reportCh chan *webrtcpb.ReportConnectionMetadataRequest
}

func (s *reportCapturingSignalingServer) ReportConnectionMetadata(
	_ context.Context,
	req *webrtcpb.ReportConnectionMetadataRequest,
) (*webrtcpb.ReportConnectionMetadataResponse, error) {
	s.reportCh <- req
	return &webrtcpb.ReportConnectionMetadataResponse{}, nil
}

// TestReportConnectionMetadataOncePerDial dials a robot over WebRTC and asserts the dialing client
// sends exactly one connection-metadata report describing the successful dial.
func TestReportConnectionMetadataOncePerDial(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	// Reporting is disabled in tests by default (see dialReportingEnabled). This test
	// exercises the reporting path, so re-enable it for the test's duration.
	origReportingEnabled := dialReportingEnabled
	dialReportingEnabled = func() bool { return true }
	defer func() { dialReportingEnabled = origReportingEnabled }()

	// The client only reports to a signaling server it classifies as the app. Treat this test's
	// loopback signaling server as an app host so the reporting path is exercised.
	origCloudHosts := viamCloudSignalingHosts
	viamCloudSignalingHosts = []string{"127.0.0.1"}
	defer func() { viamCloudSignalingHosts = origCloudHosts }()

	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() { test.That(t, signalingCallQueue.Close(), test.ShouldBeNil) }()

	const host = "yeehaw"
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger, defaultHeartbeatInterval)
	defer signalingServer.Close()

	// Capture every report the base signaling server receives.
	reportCh := make(chan *webrtcpb.ReportConnectionMetadataRequest, 8)
	capturingServer := &reportCapturingSignalingServer{WebRTCSignalingServer: signalingServer, reportCh: reportCh}

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, capturingServer)
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
	test.That(t, req.GetSignalingPath(), test.ShouldEqual, webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED)
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
