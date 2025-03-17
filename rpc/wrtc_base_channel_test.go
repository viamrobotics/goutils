package rpc

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/edaniels/golog"
	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/testutils"
)

func setupWebRTCPeers(t *testing.T) (client, server *webrtc.PeerConnection, clientDc, serverDc *webrtc.DataChannel) {
	t.Helper()
	logger := golog.NewTestLogger(t)

	pc1, dc1, err := newPeerConnectionForClient(context.Background(), webrtc.Configuration{}, true, logger)
	test.That(t, err, test.ShouldBeNil)

	encodedSDP, err := EncodeSDP(pc1.LocalDescription())
	test.That(t, err, test.ShouldBeNil)

	pc2, dc2, err := newPeerConnectionForServer(context.Background(), encodedSDP, webrtc.Configuration{}, true, logger)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, pc1.SetRemoteDescription(*pc2.LocalDescription()), test.ShouldBeNil)

	return pc1, pc2, dc1, dc2
}

func setupWebRTCBaseChannels(t *testing.T) (
	clientPC *webrtc.PeerConnection,
	serverPC *webrtc.PeerConnection,
	client *webrtcBaseChannel,
	server *webrtcBaseChannel,
) {
	t.Helper()
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)

	bc1 := newBaseChannel(context.Background(), pc1, dc1, nil, nil, logger)
	bc2 := newBaseChannel(context.Background(), pc2, dc2, nil, nil, logger)

	<-bc1.Ready()
	<-bc2.Ready()

	return pc1, pc2, bc1, bc2
}

func TestWebRTCBaseChannel(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	pc1, pc2, bc1, bc2 := setupWebRTCBaseChannels(t)

	someStatus, _ := status.FromError(errors.New("ouch"))
	test.That(t, bc1.write(someStatus.Proto()), test.ShouldBeNil)

	isClosed := bc1.Closed()
	test.That(t, isClosed, test.ShouldBeFalse)
	isClosed = bc2.Closed()
	test.That(t, isClosed, test.ShouldBeFalse)
	bc1.Close()
	pc1.GracefulClose()
	pc2.GracefulClose()

	isClosed = bc1.Closed()
	test.That(t, isClosed, test.ShouldBeTrue)
	isClosed = bc2.Closed()
	test.That(t, isClosed, test.ShouldBeTrue)
	// Double calling close poses no problems.
	bc1.Close()
	bc2.Close()

	pc1, pc2, bc1, bc2 = setupWebRTCBaseChannels(t)
	err1 := errors.New("whoops")
	bc2.Close()
	pc1.GracefulClose()
	pc2.GracefulClose()

	isClosed = bc1.Closed()
	test.That(t, isClosed, test.ShouldBeTrue)
	isClosed = bc2.Closed()
	test.That(t, isClosed, test.ShouldBeTrue)

	pc1, pc2, bc1, bc2 = setupWebRTCBaseChannels(t)
	bc2.onChannelError(err1)
	pc1.GracefulClose()
	pc2.GracefulClose()
	isClosed = bc1.Closed()
	test.That(t, isClosed, test.ShouldBeTrue)
	isClosed = bc2.Closed()
	test.That(t, isClosed, test.ShouldBeTrue)

	test.That(t, bc2.write(someStatus.Proto()), test.ShouldEqual, io.ErrClosedPipe)
}
