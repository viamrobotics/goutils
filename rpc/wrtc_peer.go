package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/pion/ice/v2"
	"github.com/pion/interceptor"
	"github.com/pion/sctp"
	"github.com/pion/webrtc/v3"
	"go.uber.org/multierr"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// DefaultICEServers is the default set of ICE servers to use for WebRTC session negotiation.
// There is no guarantee that the defaults here will remain usable.
var DefaultICEServers = []webrtc.ICEServer{
	// feel free to use your own ICE servers
	{
		URLs: []string{"stun:global.stun.twilio.com:3478"},
	},
}

// DefaultWebRTCConfiguration is the standard configuration used for WebRTC peers.
var DefaultWebRTCConfiguration = webrtc.Configuration{
	ICEServers: DefaultICEServers,
}

func newWebRTCAPI(isClient bool, logger golog.Logger) (*webrtc.API, error) {
	m := webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	i := interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(&m, &i); err != nil {
		return nil, err
	}

	var settingEngine webrtc.SettingEngine
	if isClient {
		settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryAndGather)
	} else {
		settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
	}
	// by including the loopback candidate, we allow an offline mode such that the
	// server/client (controlled/controlling) can include 127.0.0.1 as a candidate
	// while the client (controlling) provides an mDNS candidate that may resolve to 127.0.0.1.
	settingEngine.SetIncludeLoopbackCandidate(true)
	settingEngine.SetRelayAcceptanceMinWait(3 * time.Second)
	settingEngine.SetIPFilter(func(ip net.IP) bool {
		// Disallow ipv6 addresses since grpc-go does not currently support IPv6 scoped literals.
		// See related grpc-go issue: https://github.com/grpc/grpc-go/issues/3272.
		//
		// Stolen from net/ip.go, `IP.String` method.
		if p4 := ip.To4(); len(p4) == net.IPv4len {
			return true
		}

		return false
	})

	options := []func(a *webrtc.API){webrtc.WithMediaEngine(&m), webrtc.WithInterceptorRegistry(&i)}
	if utils.Debug {
		settingEngine.LoggerFactory = WebRTCLoggerFactory{logger}
	}
	options = append(options, webrtc.WithSettingEngine(settingEngine))
	return webrtc.NewAPI(options...), nil
}

func newPeerConnectionForClient(
	ctx context.Context,
	config webrtc.Configuration,
	disableTrickle bool,
	logger golog.Logger,
) (*webrtc.PeerConnection, *webrtc.DataChannel, error) {
	webAPI, err := newWebRTCAPI(true, logger)
	if err != nil {
		return nil, nil, err
	}

	peerConn, err := webAPI.NewPeerConnection(config)
	if err != nil {
		return nil, nil, err
	}
	var successful bool
	defer func() {
		if !successful {
			err = multierr.Combine(err, peerConn.Close())
		}
	}()

	negotiated := true
	ordered := true
	dataChannelID := uint16(0)
	dataChannel, err := peerConn.CreateDataChannel("data", &webrtc.DataChannelInit{
		ID:         &dataChannelID,
		Negotiated: &negotiated,
		Ordered:    &ordered,
	})
	if err != nil {
		return peerConn, nil, err
	}
	dataChannel.OnError(initialDataChannelOnError(peerConn, logger))

	// We configure "clients" for renegotation. This helper function does two things:
	// - Creates the DataChannel and `OnMessage` handlers for communicating offers+answers.
	// - Sets up an `OnNegotiationNeeded` callback to initiate an SDP change.
	//
	// Dan: The existing `OnNegotiationNeeded` algorithm is suitable when one side initiates all of
	// the renegotiations. But it is not obvious that algorithm is suitable for when both sides can
	// race on renegotiating. For now we "uninstall" the `OnNegotiationNeeded` callback and only
	// allow the "server" to start a renegotiation.
	if _, err = configureForRenegotiation(peerConn, logger); err != nil {
		return peerConn, dataChannel, err
	}
	peerConn.OnNegotiationNeeded(func() {})

	if disableTrickle {
		offer, err := peerConn.CreateOffer(nil)
		if err != nil {
			return peerConn, nil, err
		}

		// Sets the LocalDescription, and starts our UDP listeners
		err = peerConn.SetLocalDescription(offer)
		if err != nil {
			return peerConn, nil, err
		}

		// Create channel that is blocked until ICE Gathering is complete
		gatherComplete := webrtc.GatheringCompletePromise(peerConn)

		// Block until ICE Gathering is complete since we signal back one complete SDP
		// and do not want to wait on trickle ICE.
		select {
		case <-ctx.Done():
			return peerConn, nil, ctx.Err()
		case <-gatherComplete:
		}
	}

	// Will not wait for connection to establish. If you want this in the future,
	// add a state check to OnICEConnectionStateChange for webrtc.ICEConnectionStateConnected.
	successful = true
	return peerConn, dataChannel, nil
}

func newPeerConnectionForServer(
	ctx context.Context,
	sdp string,
	config webrtc.Configuration,
	disableTrickle bool,
	logger golog.Logger,
) (*webrtc.PeerConnection, *webrtc.DataChannel, error) {
	webAPI, err := newWebRTCAPI(false, logger)
	if err != nil {
		return nil, nil, err
	}

	peerConn, err := webAPI.NewPeerConnection(config)
	if err != nil {
		return nil, nil, err
	}
	var successful bool
	defer func() {
		if !successful {
			err = multierr.Combine(err, peerConn.Close())
		}
	}()

	negotiated := true
	ordered := true
	dataChannelID := uint16(0)
	dataChannel, err := peerConn.CreateDataChannel("data", &webrtc.DataChannelInit{
		ID:         &dataChannelID,
		Negotiated: &negotiated,
		Ordered:    &ordered,
	})
	if err != nil {
		return peerConn, dataChannel, err
	}
	dataChannel.OnError(initialDataChannelOnError(peerConn, logger))

	if _, err = configureForRenegotiation(peerConn, logger); err != nil {
		return peerConn, dataChannel, err
	}

	offer := webrtc.SessionDescription{}
	if err := decodeSDP(sdp, &offer); err != nil {
		return peerConn, dataChannel, err
	}

	err = peerConn.SetRemoteDescription(offer)
	if err != nil {
		return peerConn, dataChannel, err
	}

	if disableTrickle {
		answer, err := peerConn.CreateAnswer(nil)
		if err != nil {
			return peerConn, dataChannel, err
		}

		err = peerConn.SetLocalDescription(answer)
		if err != nil {
			return peerConn, dataChannel, err
		}

		// Create channel that is blocked until ICE Gathering is complete
		gatherComplete := webrtc.GatheringCompletePromise(peerConn)

		// Block until ICE Gathering is complete since we signal back one complete SDP
		// and do not want to wait on trickle ICE.
		select {
		case <-ctx.Done():
			return peerConn, nil, ctx.Err()
		case <-gatherComplete:
		}
	}

	successful = true
	return peerConn, dataChannel, nil
}

// configureForRenegotiation sets up PeerConnection callbacks for updating local descriptions and
// sending offers when a negotiation is needed (e.g: adding a video track). As well as listening for
// offers/answers to update remote descriptions (e.g: when the peer adds a video track).
//
// If successful, a Go channel is returned. The Go channel will close when the negotiation
// DataChannel is open and available for renegotiation.
func configureForRenegotiation(peerConn *webrtc.PeerConnection, logger golog.Logger) (<-chan struct{}, error) {
	var negMu sync.Mutex

	// All of Viam's PeerConnections hard code the `data` channel to be ID 0 and the `negotiation`
	// channel to be ID 1. Thus these channels are "pre-negotiated".
	negotiated := true
	// Packets over this channel must be processed in order (à la TCP).
	ordered := true
	negotiationChannelID := uint16(1)
	negotiationChannel, err := peerConn.CreateDataChannel("negotiation", &webrtc.DataChannelInit{
		ID:         &negotiationChannelID,
		Negotiated: &negotiated,
		Ordered:    &ordered,
	})
	if err != nil {
		return nil, err
	}

	negotiationChannel.OnError(initialDataChannelOnError(peerConn, logger))

	// The pion webrtc library may invoke `OnNegotiationNeeded` prior to the connection being
	// established. We drop those requests on the floor. The original connection is established with
	// our signaling and answering machinery.
	//
	// Additionally, just because a PeerConnection has moved into the `connected` state, that does
	// not imply the pre-negotiated `negotiation` DataChannel is available for use. We return this
	// `negOpened` channel to let tests create a happens-before relationship. Such that these tests
	// can know when a PeerConnection method that invokes `OnNegotiationNeeded` can utilize this
	// negotiation channel.
	negOpened := make(chan struct{})
	negotiationChannel.OnOpen(func() {
		close(negOpened)
	})

	// OnNegotiationNeeded is webrtc callback for when a PeerConnection is mutated in a way such
	// that its local description should change. Such as when a video track is added that should be
	// streamed to the peer.
	peerConn.OnNegotiationNeeded(func() {
		select {
		case <-negOpened:
		default:
			// Negotiation cannot occur over the negotiation channel until after the channel is in
			// operation.
			return
		}

		negMu.Lock()
		defer negMu.Unlock()
		// Creating an offer will generate the desired local description that includes the
		// modifications responsible for entering the callback. Such as adding a video track.
		offer, err := peerConn.CreateOffer(nil)
		if err != nil {
			logger.Errorw("renegotiation: error creating offer", "error", err)
			return
		}

		// Dan: It's not clear to me why an offer is created from a `PeerConnection` just to call
		// `PeerConnection.SetLocalDescription`. And then when encoding the `Description` ("SDP")
		// for sending to the peer, we must call `PeerConnection.LocalDescription` rather than using
		// the `offer`. But it's easy to see that the `offer` and `peerConn.LocalDescription()` are
		// different (e.g: the latter includes ICE candidates), so it must be done this way.
		if err := peerConn.SetLocalDescription(offer); err != nil {
			logger.Errorw("renegotiation: error setting local description", "error", err)
			return
		}

		// Encode and send the new local description to the peer over the `negotiation` channel. The
		// peer will respond over the negotiation channel with an answer. That answer will be used to
		// update the remote description.
		encodedSDP, err := encodeSDP(peerConn.LocalDescription())
		if err != nil {
			logger.Errorw("renegotiation: error encoding SDP", "error", err)
			return
		}
		if err := negotiationChannel.SendText(encodedSDP); err != nil {
			logger.Errorw("renegotiation: error sending SDP", "error", err)
			return
		}
	})

	negotiationChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		negMu.Lock()
		defer negMu.Unlock()

		description := webrtc.SessionDescription{}
		if err := decodeSDP(string(msg.Data), &description); err != nil {
			logger.Errorw("renegotiation: error decoding SDP", "error", err)
			return
		}

		// A new description was received over the negotiation channel. Use that to update the remote
		// description.
		if err := peerConn.SetRemoteDescription(description); err != nil {
			logger.Errorw("renegotiation: error setting remote description", "error", err)
			return
		}

		// If the message was an offer, generate an answer, set it as the local description and
		// respond. Such that the peer can update its remote description.
		//
		// If the incoming message was an answer, the peers are now in sync and no further messages
		// are required.
		if description.Type != webrtc.SDPTypeOffer {
			return
		}

		// Dan: It's unclear to me how error handling should happen here. Receiving an offer implies
		// the peer's local description is not in sync with our remote description for that
		// peer. I'm unsure of the long-term consequence of a pair of PeerConnections being in this
		// inconsistent state.
		answer, err := peerConn.CreateAnswer(nil)
		if err != nil {
			logger.Errorw("renegotiation: error creating answer", "error", err)
			return
		}
		if err := peerConn.SetLocalDescription(answer); err != nil {
			logger.Errorw("renegotiation: error setting local description", "error", err)
			return
		}

		encodedSDP, err := encodeSDP(peerConn.LocalDescription())
		if err != nil {
			logger.Errorw("renegotiation: error encoding SDP", "error", err)
			return
		}
		if err := negotiationChannel.SendText(encodedSDP); err != nil {
			logger.Errorw("renegotiation: error sending SDP", "error", err)
			return
		}
	})

	return negOpened, nil
}

type webrtcPeerConnectionStats struct {
	ID               string
	RemoteCandidates map[string]string
}

func webrtcPeerConnCandPair(peerConnection *webrtc.PeerConnection) (*webrtc.ICECandidatePair, bool) {
	connectionState := peerConnection.ICEConnectionState()
	if connectionState == webrtc.ICEConnectionStateConnected && peerConnection.SCTP() != nil &&
		peerConnection.SCTP().Transport() != nil &&
		peerConnection.SCTP().Transport().ICETransport() != nil {
		candPair, err := peerConnection.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil {
			return nil, false
		}
		return candPair, true
	}
	return nil, false
}

func getWebRTCPeerConnectionStats(peerConnection *webrtc.PeerConnection) webrtcPeerConnectionStats {
	stats := peerConnection.GetStats()
	var connID string
	connInfo := map[string]string{}
	for _, stat := range stats {
		if pcStats, ok := stat.(webrtc.PeerConnectionStats); ok {
			connID = pcStats.ID
		}
		candidateStats, ok := stat.(webrtc.ICECandidateStats)
		if !ok {
			continue
		}
		if candidateStats.Type != webrtc.StatsTypeRemoteCandidate {
			continue
		}
		var candidateType string
		switch candidateStats.CandidateType {
		case webrtc.ICECandidateTypeRelay:
			candidateType = "relay"
		case webrtc.ICECandidateTypePrflx:
			candidateType = "peer-reflexive"
		case webrtc.ICECandidateTypeSrflx:
			candidateType = "server-reflexive"
		case webrtc.ICECandidateTypeHost:
			candidateType = "host"
		}
		if candidateType == "" {
			continue
		}
		connInfo[candidateType] = candidateStats.IP
	}
	return webrtcPeerConnectionStats{connID, connInfo}
}

func initialDataChannelOnError(pc io.Closer, logger golog.Logger) func(err error) {
	return func(err error) {
		if errors.Is(err, sctp.ErrResetPacketInStateNotExist) ||
			isUserInitiatedAbortChunkErr(err) {
			return
		}
		logger.Errorw("premature data channel error before WebRTC channel association", "error", err)
		utils.UncheckedError(pc.Close())
	}
}

func iceCandidateToProto(i *webrtc.ICECandidate) *webrtcpb.ICECandidate {
	return iceCandidateInitToProto(i.ToJSON())
}

func iceCandidateInitToProto(ij webrtc.ICECandidateInit) *webrtcpb.ICECandidate {
	candidate := webrtcpb.ICECandidate{
		Candidate: ij.Candidate,
	}
	if ij.SDPMid != nil {
		val := *ij.SDPMid
		candidate.SdpMid = &val
	}
	if ij.SDPMLineIndex != nil {
		val := uint32(*ij.SDPMLineIndex)
		candidate.SdpmLineIndex = &val
	}
	if ij.UsernameFragment != nil {
		val := *ij.UsernameFragment
		candidate.UsernameFragment = &val
	}
	return &candidate
}

func iceCandidateFromProto(i *webrtcpb.ICECandidate) webrtc.ICECandidateInit {
	candidate := webrtc.ICECandidateInit{
		Candidate: i.Candidate,
	}
	if i.SdpMid != nil {
		val := *i.SdpMid
		candidate.SDPMid = &val
	}
	if i.SdpmLineIndex != nil {
		val := uint16(*i.SdpmLineIndex)
		candidate.SDPMLineIndex = &val
	}
	if i.UsernameFragment != nil {
		val := *i.UsernameFragment
		candidate.UsernameFragment = &val
	}
	return candidate
}
