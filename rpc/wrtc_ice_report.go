package rpc

import (
	"context"
	"strings"
	"time"

	"github.com/viamrobotics/webrtc/v3"
	"google.golang.org/grpc/metadata"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// reportSelectedICECandidateType reports the selected ICE candidate type to the signaling server
// as a best-effort, fire-and-forget operation. Errors are logged at debug level and do not affect
// the connection.
func reportSelectedICECandidateType(
	ctx context.Context,
	host string,
	signalingClient webrtcpb.SignalingServiceClient,
	peerConn *webrtc.PeerConnection,
	logger utils.ZapCompatibleLogger,
) {
	candidateType := selectedICECandidateType(peerConn.GetStats())
	if candidateType == webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED {
		logger.Debugw("could not determine selected ICE candidate type, skipping report")
		return
	}

	reportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reportCtx = metadata.NewOutgoingContext(reportCtx, metadata.New(map[string]string{RPCHostMetadataField: host}))

	if _, err := signalingClient.ReportICECandidateSelected(reportCtx, &webrtcpb.ReportICECandidateSelectedRequest{
		CandidateType: candidateType,
	}); err != nil {
		logger.Debugw("failed to report selected ICE candidate type", "err", err)
	}
}

// selectedICECandidateType inspects a WebRTC stats report to determine which ICE candidate type
// was selected for the connection. It finds the nominated candidate pair and examines the remote
// candidate to classify the connection.
func selectedICECandidateType(stats webrtc.StatsReport) webrtcpb.ICECandidateType {
	// Find the nominated candidate pair and get its remote candidate ID.
	remoteCandID := ""
	for _, stat := range stats {
		pair, ok := stat.(webrtc.ICECandidatePairStats)
		if !ok || !pair.Nominated {
			continue
		}
		remoteCandID = pair.RemoteCandidateID
		break
	}
	if remoteCandID == "" {
		return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED
	}

	remoteStat, ok := stats[remoteCandID]
	if !ok {
		return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED
	}
	remoteCand, ok := remoteStat.(webrtc.ICECandidateStats)
	if !ok {
		return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED
	}

	//nolint:exhaustive
	switch remoteCand.CandidateType {
	case webrtc.ICECandidateTypeHost:
		return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_HOST
	case webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypePrflx:
		return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_STUN
	case webrtc.ICECandidateTypeRelay:
		// Distinguish Viam's coturn server from Twilio (or other external) TURN servers by URL.
		if strings.Contains(remoteCand.URL, "viam.com") || strings.Contains(remoteCand.URL, "viaminternal") {
			return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_COTURN_RELAY
		}
		return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_TWILIO_RELAY
	}

	return webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED
}
