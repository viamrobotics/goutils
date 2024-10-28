package rpc

import (
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/viamrobotics/webrtc/v3"
	"github.com/viamrobotics/webrtc/v3/pkg/media"
	"go.uber.org/atomic"
	"go.viam.com/test"
)

// Perform signaling and answering necessary to establish the connection. Note, this function can
// return before the input `PeerConnection` state reports `PeerConnectionStateConnected`. That state
// isn't considered useful for callers. Notably because being connected does not imply it is safe to
// start sending messages over pre-negotiated `DataChannel`s.
func signalPair(t *testing.T, left, right *webrtc.PeerConnection) {
	t.Helper()

	leftOffer, err := left.CreateOffer(nil)
	test.That(t, err, test.ShouldBeNil)
	err = left.SetLocalDescription(leftOffer)
	test.That(t, err, test.ShouldBeNil)
	<-webrtc.GatheringCompletePromise(left)

	leftOffer.SDP = left.LocalDescription().SDP
	err = right.SetRemoteDescription(leftOffer)
	test.That(t, err, test.ShouldBeNil)

	rightAnswer, err := right.CreateAnswer(nil)
	test.That(t, err, test.ShouldBeNil)
	err = right.SetLocalDescription(rightAnswer)
	test.That(t, err, test.ShouldBeNil)
	<-webrtc.GatheringCompletePromise(right)

	err = left.SetRemoteDescription(rightAnswer)
	test.That(t, err, test.ShouldBeNil)
}

func TestRenegotation(t *testing.T) {
	logger := golog.NewTestLogger(t)

	var (
		clientNegChannelOpened <-chan struct{}
		clientNegChannelClosed <-chan struct{}
		serverNegChannelOpened <-chan struct{}
		serverNegChannelClosed <-chan struct{}
	)

	// A raw `webrtc.PeerConnection` is suitable for this test. As opposed to our helper
	// constructors.
	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		client.GracefulClose()
		<-clientNegChannelClosed
	}()

	server, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		server.GracefulClose()
		<-serverNegChannelClosed
	}()

	// Add a renegotation channel. Set these channels up before signaling/answering.
	clientNegChannelOpened, clientNegChannelClosed, err = ConfigureForRenegotiation(client, PeerRoleClient, logger)
	test.That(t, err, test.ShouldBeNil)

	serverNegChannelOpened, serverNegChannelClosed, err = ConfigureForRenegotiation(server, PeerRoleServer, logger)
	test.That(t, err, test.ShouldBeNil)

	// Run signaling/answering such that the client + server can connect to each other.
	signalPair(t, client, server)

	// Wait for the negotiation channels to be ready.
	<-clientNegChannelOpened
	<-serverNegChannelOpened

	// This test observes a successful renegotiation by having a server create a video track, and
	// communicating this to the client via the `negotiation` DataChannel. And then sending data
	// over the video track. Install the `OnTrack` callback before kicking off the renegotiation via
	// the (server) `AddTrack` call.
	onTrack := atomic.Bool{}
	client.OnTrack(func(track *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
		onTrack.Store(true)
	})

	trackLocal, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/H264"},
		"video", "main+camera")
	test.That(t, err, test.ShouldBeNil)

	// `AddTrack` triggers the `PeerConnection.OnNegotiationNeeded` callback. Which will
	// asynchronously start our custom renegotiation code that sends offer/answer messages over the
	// `negotiation` DataChannel.
	_, err = server.AddTrack(trackLocal)
	test.That(t, err, test.ShouldBeNil)

	// Send data over the track until we observe the `OnTrack` callback is invoked.
	//
	// Dan: I gave this logic a very lenient 10 second timeout to work before determining the test
	// is a failure. Locally, this loop works after ~20-30ms. The extra budget is FUD (fear,
	// uncertainty, doubt) over the performance of our CI runners, or running under CPU emulation.
	for start := time.Now(); time.Since(start) < 10*time.Second; {
		err = trackLocal.WriteSample(media.Sample{Data: []byte{0, 0, 0, 0, 0}, Timestamp: time.Now(), Duration: time.Millisecond})
		test.That(t, err, test.ShouldBeNil)
		if onTrack.Load() == true {
			break
		}
		time.Sleep(time.Millisecond)
	}
	test.That(t, onTrack.Load(), test.ShouldBeTrue)
}
