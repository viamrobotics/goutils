package rpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

// TestDialUnauthenticatedCaller exercises the resilient signaling path, making sure that
// robots can authenticate unauthenticated callers themselves when the signaling server cannot auth
func TestDialUnauthenticatedCaller(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	const host, keyID, key = "robot-host", "key-id", "key-secret"

	var authWorks atomic.Bool
	authWorks.Store(true)
	flakyAuth := AuthHandlerFunc(func(context.Context, string, string) (map[string]string, error) {
		if authWorks.Load() {
			return map[string]string{}, nil
		}
		return nil, status.Error(codes.Unavailable, "auth is down")
	})

	// Signaling server: callers and answerers authenticate through flakyAuth; the caller
	// methods are public so a caller with no token still reaches Call.
	signalingListener := listen(t)
	signalingServer, err := NewServer(logger,
		WithAuthHandler("robot-secret", flakyAuth),
		WithAuthHandler(CredentialsTypeAPIKey, flakyAuth),
		WithPublicMethods([]string{
			webrtcpb.SignalingService_OptionalWebRTCConfig_FullMethodName,
			webrtcpb.SignalingService_Call_FullMethodName,
			webrtcpb.SignalingService_CallUpdate_FullMethodName,
		}),
	)
	test.That(t, err, test.ShouldBeNil)
	defer signalingServer.Stop()
	queue := NewMemoryWebRTCCallQueue(logger)
	defer queue.Close()
	test.That(t, signalingServer.RegisterServiceServer(context.Background(),
		&webrtcpb.SignalingService_ServiceDesc,
		NewWebRTCSignalingServer(queue, nil, logger, defaultHeartbeatInterval),
		webrtcpb.RegisterSignalingServiceHandlerFromEndpoint,
	), test.ShouldBeNil)
	test.That(t, signalingServer.Serve(signalingListener), test.ShouldBeNil)

	// Answerer: knows one API key, so it advertises can_auth_callers and can verify a token.
	robotServer, err := NewServer(logger,
		WithInstanceNames(host),
		WithAuthHandler(CredentialsTypeAPIKey, MakeSimpleMultiAuthPairHandler(map[string]string{keyID: key})),
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                   true,
			ExternalSignalingHosts:   []string{host},
			ExternalSignalingAddress: signalingListener.Addr().String(),
			ExternalSignalingDialOpts: []DialOption{
				WithInsecure(),
				WithEntityCredentials(host, Credentials{Type: "robot-secret", Payload: "shh"}),
			},
		}),
	)
	test.That(t, err, test.ShouldBeNil)
	defer robotServer.Stop()
	test.That(t, robotServer.RegisterServiceServer(context.Background(),
		&pb.EchoService_ServiceDesc, &echoserver.Server{}, pb.RegisterEchoServiceHandlerFromEndpoint,
	), test.ShouldBeNil)
	test.That(t, robotServer.Serve(listen(t)), test.ShouldBeNil)

	dial := func(apiKey string, allowUnauth bool) (ClientConn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		creds := Credentials{Type: CredentialsTypeAPIKey, Payload: apiKey}
		return Dial(ctx, host, logger,
			WithEntityCredentials(keyID, creds),
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingServerAddress:        signalingListener.Addr().String(),
				SignalingInsecure:             true,
				SignalingAuthEntity:           keyID,
				SignalingCreds:                creds,
				AllowUnauthenticatedSignaling: allowUnauth,
			}),
		)
	}
	echo := func(conn ClientConn) error {
		_, err := pb.NewEchoServiceClient(conn).Echo(context.Background(), &pb.EchoRequest{Message: "hi"})
		return err
	}

	t.Run("auth up: normal authenticated call", func(t *testing.T) {
		conn, err := dial(key, false)
		test.That(t, err, test.ShouldBeNil)
		defer conn.Close()
		test.That(t, echo(conn), test.ShouldBeNil)
	})

	authWorks.Store(false)

	t.Run("auth down, no fallback: dial fails at Authenticate", func(t *testing.T) {
		_, err := dial(key, false)
		test.That(t, status.Code(err), test.ShouldEqual, codes.Unavailable)
	})

	t.Run("auth down, fallback, right key: answerer verifies the token", func(t *testing.T) {
		conn, err := dial(key, true)
		test.That(t, err, test.ShouldBeNil)
		defer conn.Close()
		test.That(t, echo(conn), test.ShouldBeNil)
	})

	t.Run("auth down, fallback, wrong key: answerer refuses every RPC", func(t *testing.T) {
		conn, err := dial("wrong", true)
		test.That(t, err, test.ShouldBeNil)
		defer conn.Close()
		test.That(t, status.Code(echo(conn)), test.ShouldEqual, codes.Unauthenticated)
	})
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	return listener
}
