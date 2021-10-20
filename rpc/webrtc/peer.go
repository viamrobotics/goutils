package rpcwebrtc

import (
	"context"
	"io"

	"github.com/edaniels/golog"
	"github.com/edaniels/gostream"
	gwebrtc "github.com/edaniels/gostream/webrtc"
	"github.com/pion/webrtc/v3"
	"go.uber.org/multierr"

	"go.viam.com/utils"
)

// DefaultWebRTCConfiguration is the standard configuration used for WebRTC peers.
var DefaultWebRTCConfiguration = webrtc.Configuration{
	ICEServers: gostream.DefaultICEServers,
}

func newPeerConnectionForClient(ctx context.Context, config webrtc.Configuration, logger golog.Logger) (pc *webrtc.PeerConnection, dc *webrtc.DataChannel, err error) {
	pc, err = webrtc.NewPeerConnection(DefaultWebRTCConfiguration)
	if err != nil {
		return nil, nil, err
	}
	var successful bool
	defer func() {
		if !successful {
			err = multierr.Combine(err, pc.Close())
		}
	}()

	negotiated := true
	ordered := true
	dataChannelID := uint16(0)
	dataChannel, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		ID:         &dataChannelID,
		Negotiated: &negotiated,
		Ordered:    &ordered,
	})
	if err != nil {
		return pc, nil, err
	}
	dataChannel.OnError(initialDataChannelOnError(pc, logger))

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return pc, nil, err
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(pc)

	// Sets the LocalDescription, and starts our UDP listeners
	err = pc.SetLocalDescription(offer)
	if err != nil {
		return pc, nil, err
	}

	// Block until ICE Gathering is complete since we signal back one complete SDP
	// and do not want to wait on trickle ICE.
	select {
	case <-ctx.Done():
		return pc, nil, ctx.Err()
	case <-gatherComplete:
	}

	// Will not wait for connection to establish. If you want this in the future,
	// add a state check to OnICEConnectionStateChange for webrtc.ICEConnectionStateConnected.
	successful = true
	return pc, dataChannel, nil
}

func newPeerConnectionForServer(ctx context.Context, sdp string, logger golog.Logger) (pc *webrtc.PeerConnection, dc *webrtc.DataChannel, err error) {
	pc, err = webrtc.NewPeerConnection(DefaultWebRTCConfiguration)
	if err != nil {
		return nil, nil, err
	}
	var successful bool
	defer func() {
		if !successful {
			err = multierr.Combine(err, pc.Close())
		}
	}()

	negotiated := true
	ordered := true
	dataChannelID := uint16(0)
	dataChannel, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		ID:         &dataChannelID,
		Negotiated: &negotiated,
		Ordered:    &ordered,
	})
	if err != nil {
		return pc, dataChannel, err
	}
	dataChannel.OnError(initialDataChannelOnError(pc, logger))

	offer := webrtc.SessionDescription{}
	if err := gwebrtc.DecodeSDP(sdp, &offer); err != nil {
		return pc, dataChannel, err
	}

	err = pc.SetRemoteDescription(offer)
	if err != nil {
		return pc, dataChannel, err
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return pc, dataChannel, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)

	err = pc.SetLocalDescription(answer)
	if err != nil {
		return pc, dataChannel, err
	}

	// Block until ICE Gathering is complete since we signal back one complete SDP
	// and do not want to wait on trickle ICE.
	select {
	case <-ctx.Done():
		return pc, dataChannel, ctx.Err()
	case <-gatherComplete:
	}

	successful = true
	return pc, dataChannel, nil
}

type peerConnectionStats struct {
	ID               string
	RemoteCandidates map[string]string
}

func getPeerConnectionStats(peerConnection *webrtc.PeerConnection) peerConnectionStats {
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
		}
		if candidateType == "" {
			continue
		}
		connInfo[candidateType] = candidateStats.IP
	}
	return peerConnectionStats{connID, connInfo}
}

func initialDataChannelOnError(pc io.Closer, logger golog.Logger) func(err error) {
	return func(err error) {
		logger.Errorw("premature data channel error before WebRTC channel association", "error", err)
		utils.UncheckedError(pc.Close())
	}
}
