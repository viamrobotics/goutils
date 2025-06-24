package rpc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"github.com/pion/stun"
	"github.com/samber/lo"
	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.viam.com/utils"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestWebRTCSignalingWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCSignaling(t, signalingCallQueue, logger)
}

func TestWebRTCSignalingWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCSignaling(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCSignaling(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	hosts := []string{"yeehaw", "woahthere"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
				defaultHeartbeatInterval)
			defer signalingServer.Close()

			grpcListener, err := net.Listen("tcp", "localhost:0")
			test.That(t, err, test.ShouldBeNil)
			grpcServer := grpc.NewServer()
			grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

			serveDone := make(chan error)
			go func() {
				serveDone <- grpcServer.Serve(grpcListener)
			}()

			webrtcServer := newWebRTCServer(logger)
			webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

			answerer := newWebRTCSignalingAnswerer(
				grpcListener.Addr().String(),
				hosts,
				webrtcServer,
				[]DialOption{WithInsecure()},
				webrtc.Configuration{},
				logger,
			)
			answerer.Start()

			//nolint:staticcheck
			cc, err := grpc.Dial(
				grpcListener.Addr().String(),
				grpc.WithBlock(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			test.That(t, err, test.ShouldBeNil)
			defer func() {
				test.That(t, cc.Close(), test.ShouldBeNil)
			}()
			signalClient := webrtcpb.NewSignalingServiceClient(cc)

			callClient, err := signalClient.Call(context.Background(), &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "expected rpc-host")

			md := metadata.New(map[string]string{"rpc-host": ""})
			callCtx := metadata.NewOutgoingContext(context.Background(), md)
			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "non-empty rpc-host")

			md = metadata.New(map[string]string{"rpc-host": "one"})
			md.Append("rpc-host", "two")
			callCtx = metadata.NewOutgoingContext(context.Background(), md)
			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "expected 1 rpc-host")

			md = metadata.New(map[string]string{"rpc-host": host})
			callCtx = metadata.NewOutgoingContext(context.Background(), md)

			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "unexpected")

			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{Sdp: "thing"})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "illegal")

			for _, tc := range []bool{true, false} {
				t.Run(fmt.Sprintf("with trickle disabled %t", tc), func(t *testing.T) {
					ch, err := DialWebRTC(
						context.Background(),
						grpcListener.Addr().String(),
						host,
						logger,
						WithWebRTCOptions(DialWebRTCOptions{
							SignalingInsecure: true,
							DisableTrickleICE: tc,
						}),
					)
					test.That(t, err, test.ShouldBeNil)
					defer func() {
						test.That(t, ch.Close(), test.ShouldBeNil)
					}()

					echoClient := echopb.NewEchoServiceClient(ch)
					resp, err := echoClient.Echo(context.Background(), &echopb.EchoRequest{Message: "hello"})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, resp.GetMessage(), test.ShouldEqual, "hello")
				})
			}

			// Mimic order of stopping used in `simpleServer.Stop()` (answerer, sig
			// server's gRPC listener, then machine).
			answerer.Stop()
			grpcServer.Stop()
			webrtcServer.Stop()
			test.That(t, <-serveDone, test.ShouldBeNil)
		})
	}
}

func TestWebRTCAnswererImmediateStop(t *testing.T) {
	// Primarily a regression test for RSDK-3441.

	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()

	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	webrtcServer := newWebRTCServer(logger)
	defer webrtcServer.Stop()
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	hosts := []string{"foo", "bar"}
	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		hosts,
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)

	// Running both asynchronously means Stop will potentially happen before Start,
	// but this setup still ensures that the two methods do not race each other.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		answerer.Start()
	}()
	go func() {
		defer wg.Done()
		answerer.Stop()
	}()
	wg.Wait()
}

func TestSignalingHeartbeats(t *testing.T) {
	logger, observer := golog.NewObservedTestLogger(t)

	// Create a simple signaling server with an in-memory call-queue.
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	// Use a lowered heartbeatInterval (500ms instead of 15s).
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		500*time.Millisecond)
	defer signalingServer.Close()
	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)
	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	// Create a simple WebRTC server that (needlessly) serves the Echo service.
	// Start an answerer with it.
	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})
	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		[]string{"foo"},
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)
	answerer.Start()

	// Assert that the answerer eventually logs received heartbeats.
	testutils.WaitForAssertion(t, func(tb testing.TB) {
		t.Helper()
		test.That(tb, observer.FilterMessageSnippet(heartbeatReceivedLog).Len(),
			test.ShouldBeGreaterThan, 0)
	})

	// Mimic order of stopping used in `simpleServer.Stop()` (answerer, sig
	// server's gRPC listener, then machine).
	answerer.Stop()
	grpcServer.Stop()
	webrtcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}

func TestGetDeadline(t *testing.T) {
	logger := golog.NewTestLogger(t)
	ctx := context.Background()
	initStage := &webrtcpb.AnswerRequest_Init{
		Init: &webrtcpb.AnswerRequestInitStage{},
	}
	type testcase struct {
		name  string        // name of the test
		delta time.Duration // how far in the future to set deadline
	}

	for _, tc := range []testcase{
		// case 1: default offer deadline
		{"default", _defaultOfferDeadline},
		// case 2: simulate a machine with bad system clock, deadline is less than 5 seconds
		{"too-soon", time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cancel func()
			initStage.Init.Deadline = timestamppb.New(time.Now().Add(tc.delta))
			ctx, cancel = getDeadline(ctx, logger, initStage) //nolint:fatcontext
			defer cancel()
			deadline, ok := ctx.Deadline()
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, time.Until(deadline), test.ShouldBeGreaterThan, time.Second*4)
		})
	}
}

func TestExtendWebRTCConfig(t *testing.T) {
	logger := golog.NewTestLogger(t)
	t.Run("add ice servers", func(t *testing.T) {
		cfg := &webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{
					URLs: []string{"stun:stun.example.com:3478"},
				},
			},
		}
		extra := &webrtcpb.WebRTCConfig{
			AdditionalIceServers: []*webrtcpb.ICEServer{
				{
					Urls:       []string{"turn:turn.example.com:3478"},
					Username:   "foo",
					Credential: "bar",
				},
			},
		}
		extended := extendWebRTCConfig(logger, cfg, extra, extendWebRTCConfigOptions{})
		test.That(t, extended.ICEServers, test.ShouldHaveLength, 2)
		turnServers := lo.Filter(extended.ICEServers, func(server webrtc.ICEServer, _ int) bool {
			return strings.HasPrefix(server.URLs[0], "turn")
		})
		test.That(t, turnServers, test.ShouldHaveLength, 1)
		test.That(t, turnServers[0].URLs, test.ShouldHaveLength, 1)
		test.That(t, turnServers[0].URLs[0], test.ShouldEqual, "turn:turn.example.com:3478")
		test.That(t, turnServers[0].Username, test.ShouldEqual, "foo")
		test.That(t, turnServers[0].Credential, test.ShouldEqual, "bar")
	})
	t.Run("add filtered ice server", func(t *testing.T) {
		cfg := &webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{
					URLs: []string{"stun:stun.example.com:3478"},
				},
			},
		}
		extraMulti := &webrtcpb.WebRTCConfig{
			AdditionalIceServers: []*webrtcpb.ICEServer{
				{
					Urls:       []string{"turn:turn.example.com:3478"},
					Username:   "foo",
					Credential: "bar",
				},
				{
					Urls:       []string{"turn:turn2.example.com:443"},
					Username:   "foo",
					Credential: "baz",
				},
				{
					Urls:       []string{"turn:turn3.example.com:8080"},
					Username:   "bar",
					Credential: "foo",
				},
			},
		}
		t.Run("no match", func(t *testing.T) {
			filterURI, err := stun.ParseURI("turn:turn2.example.com:3478")
			test.That(t, err, test.ShouldBeNil)
			extended := extendWebRTCConfig(logger, cfg, extraMulti, extendWebRTCConfigOptions{
				turnURI: filterURI,
			})
			test.That(t, extended.ICEServers, test.ShouldHaveLength, 1)
			test.That(t, extended.ICEServers[0].URLs[0], test.ShouldNotStartWith, "turn")
		})
		t.Run("match", func(t *testing.T) {
			filterURI, err := stun.ParseURI(extraMulti.AdditionalIceServers[1].Urls[0])
			test.That(t, err, test.ShouldBeNil)
			extended := extendWebRTCConfig(logger, cfg, extraMulti, extendWebRTCConfigOptions{
				turnURI: filterURI,
			})
			test.That(t, extended.ICEServers, test.ShouldHaveLength, 2)
			turnServers := lo.Filter(extended.ICEServers, func(server webrtc.ICEServer, _ int) bool {
				return strings.HasPrefix(server.URLs[0], "turn")
			})
			test.That(t, turnServers, test.ShouldHaveLength, 1)
			test.That(t, turnServers[0].URLs, test.ShouldHaveLength, 1)
			test.That(t, turnServers[0].URLs[0], test.ShouldEqual, "turn:turn2.example.com:443?transport=udp")
			test.That(t, turnServers[0].Username, test.ShouldEqual, "foo")
			test.That(t, turnServers[0].Credential, test.ShouldEqual, "baz")
		})
	})

	table := []struct {
		name            string
		opts            extendWebRTCConfigOptions
		expectedTurnURI string
	}{
		{
			name: "replace turn with turns",
			opts: extendWebRTCConfigOptions{
				turnScheme: stun.SchemeTypeTURNS,
			},
			expectedTurnURI: "turns:turn.example.com:3478?transport=udp",
		},
		{
			name: "replace udp with tcp",
			opts: extendWebRTCConfigOptions{
				replaceUDPWithTCP: true,
			},
			expectedTurnURI: "turn:turn.example.com:3478?transport=tcp",
		},
		{
			name: "replace port",
			opts: extendWebRTCConfigOptions{
				turnPort: 443,
			},
			expectedTurnURI: "turn:turn.example.com:443?transport=udp",
		},
	}
	for _, row := range table {
		t.Run(row.name, func(t *testing.T) {
			cfg := &webrtc.Configuration{
				ICEServers: []webrtc.ICEServer{
					{
						URLs: []string{"stun:stun.example.com:3478"},
					},
				},
			}
			extra := &webrtcpb.WebRTCConfig{
				AdditionalIceServers: []*webrtcpb.ICEServer{
					{
						Urls:       []string{"turn:turn.example.com:3478?transport=udp"},
						Username:   "foo",
						Credential: "bar",
					},
				},
			}
			extended := extendWebRTCConfig(logger, cfg, extra, row.opts)
			test.That(t, extended.ICEServers, test.ShouldHaveLength, 2)
			turnServers := lo.Filter(extended.ICEServers, func(server webrtc.ICEServer, _ int) bool {
				return strings.HasPrefix(server.URLs[0], "turn")
			})
			test.That(t, turnServers, test.ShouldHaveLength, 1)
			test.That(t, turnServers[0].URLs, test.ShouldHaveLength, 1)
			test.That(t, turnServers[0].URLs[0], test.ShouldEqual, row.expectedTurnURI)
			test.That(t, turnServers[0].Username, test.ShouldEqual, "foo")
			test.That(t, turnServers[0].Credential, test.ShouldEqual, "bar")
			stunServers := lo.Filter(extended.ICEServers, func(server webrtc.ICEServer, _ int) bool {
				return strings.HasPrefix(server.URLs[0], "stun")
			})
			test.That(t, stunServers, test.ShouldHaveLength, 1)
			test.That(t, stunServers[0].URLs, test.ShouldHaveLength, 1)
			test.That(t, stunServers[0].URLs[0], test.ShouldEqual, "stun:stun.example.com:3478")
			test.That(t, stunServers[0].Username, test.ShouldEqual, "")
			test.That(t, stunServers[0].Credential, test.ShouldEqual, nil)
		})
	}
}
