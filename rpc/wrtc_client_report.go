package rpc

import (
	"context"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/viamrobotics/webrtc/v3"
	"google.golang.org/grpc/metadata"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// SelectedCandidatePair returns the ICE candidate pair a WebRTC connection settled on — the
// nominated pair in the succeeded state — from its stats report, and whether such a pair exists.
// Obtain the report from (*webrtc.PeerConnection).GetStats().
func SelectedCandidatePair(stats webrtc.StatsReport) (webrtc.ICECandidatePairStats, bool) {
	for _, stat := range stats {
		pair, ok := stat.(webrtc.ICECandidatePairStats)
		if !ok || !pair.Nominated || pair.State != webrtc.StatsICECandidatePairStateSucceeded {
			continue
		}
		return pair, true
	}
	return webrtc.ICECandidatePairStats{}, false
}

// fixUpReportDialOpts derives dial options for how to reach the app signaling server to deliver the report,
// from the primary (non-mDNS) dial attempt's own options to the signaling server. A dial already signaling
// through app has its options unchanged; a raw IP or .local dial is redirected to prod app, reusing the
// credentials this attempt authed with. A robot not registered against prod app has its report dropped.
func fixUpReportDialOpts(dOpts dialOptions) *dialOptions {
	if classifySignalingPath(dOpts.webrtcOpts.SignalingServerAddress, dOpts.usingMDNS) !=
		webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED {
		if dOpts.webrtcOpts.SignalingCreds.Type == "" {
			return nil
		}
		dOpts.webrtcOpts.SignalingServerAddress = "app.viam.com:443"
		dOpts.webrtcOpts.SignalingInsecure = false // app is always TLS
	}
	return &dOpts
}

const dialReportTimeout = 2 * time.Second

// sendDialReport delivers a single connection report for a logical dial. It is called synchronously
// at the end of a dial but detaches from the dial context (so a cancelled or timed-out dial still reports),
// bounded by dialReportTimeout. It opens its own connection to the app signaling server, stamps the RPC
// host, and logs any errors. report may be nil (a dial that produced no attempts, e.g. a cache hit).
func sendDialReport(
	ctx context.Context,
	host string,
	logger utils.ZapCompatibleLogger,
	report *dialReport,
	dialErr error,
) {
	if report == nil {
		return
	}
	req := selectReport(report.reqs, dialErr)
	if req == nil || report.appDialOpts == nil {
		return
	}
	appDialOpts := report.appDialOpts

	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dialReportTimeout)
	defer cancel()

	conn, err := dialSignalingServer(reportCtx, appDialOpts.webrtcOpts.SignalingServerAddress, host, logger, *appDialOpts)
	if err != nil {
		logger.Debugw("failed to connect to app signaling server to report connection metadata", "err", err)
		return
	}
	defer func() { utils.UncheckedError(conn.Close()) }()

	reportCtx = metadata.NewOutgoingContext(reportCtx, metadata.New(map[string]string{RPCHostMetadataField: host}))
	client := webrtcpb.NewSignalingServiceClient(conn)
	if _, err := client.ReportConnectionMetadata(reportCtx, req); err != nil {
		logger.Debugw("failed to report connection metadata", "reached_stage", req.GetReachedStage(), "err", err)
	}
}

// selectReport picks the single report to deliver for a dial, or nil if none should be sent. It takes
// the furthest-progressed attempt — READY has the highest stage and indicates success, otherwise take
// the latest failure stage. dialErr is the logical dial's outcome: when it is nil, only a READY report
// is truthful, so a non-READY furthest (e.g. the winning attempt reused a cached connection and made
// no report of its own) is suppressed rather than counting a failure against a dial that succeeded.
func selectReport(
	reqs []*webrtcpb.ReportConnectionMetadataRequest,
	dialErr error,
) *webrtcpb.ReportConnectionMetadataRequest {
	var best *webrtcpb.ReportConnectionMetadataRequest
	for _, r := range reqs {
		if best == nil || r.GetReachedStage() > best.GetReachedStage() {
			best = r
		}
	}
	if best == nil || (dialErr == nil && best.GetReachedStage() != webrtcpb.DialStage_DIAL_STAGE_READY) {
		return nil
	}
	return best
}

// dialDurationMS returns milliseconds elapsed since start.
func dialDurationMS(start time.Time) uint32 {
	return uint32(time.Since(start).Milliseconds())
}

// viamCloudSignalingHosts are the Viam app signaling server hosts.
var viamCloudSignalingHosts = []string{"app.viam.com", "app.viam.dev"}

// classifySignalingPath derives how a connection was signaled from the signaling address. mDNS
// discovery -> MDNS_LOCAL; a Viam app signaling host -> CLOUD_SIGNALED; everything else (localhost,
// private/LAN addresses, etc.) -> LOCAL.
func classifySignalingPath(signalingAddress string, usingMDNS bool) webrtcpb.ConnectionSignalingPath {
	if usingMDNS {
		return webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_MDNS_LOCAL
	}
	host := signalingAddress
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if slices.Contains(viamCloudSignalingHosts, strings.ToLower(host)) {
		return webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED
	}
	return webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL
}

// classifyConnection inspects the selected ICE candidate pair and classifies each side into a
// ConnectionCandidate. Both are UNSPECIFIED when peerConn is nil (a failed dial) or no succeeded,
// nominated pair exists.
func classifyConnection(peerConn *webrtc.PeerConnection) (local, remote *webrtcpb.ConnectionCandidate) {
	if peerConn == nil {
		return &webrtcpb.ConnectionCandidate{}, &webrtcpb.ConnectionCandidate{}
	}
	stats := peerConn.GetStats()
	pair, ok := SelectedCandidatePair(stats)
	if !ok {
		return &webrtcpb.ConnectionCandidate{}, &webrtcpb.ConnectionCandidate{}
	}
	return classifyCandidate(stats, pair.LocalCandidateID), classifyCandidate(stats, pair.RemoteCandidateID)
}

// classifyCandidate maps a single ICE candidate stat to a ConnectionCandidate; a missing or
// unrecognized candidate yields type UNSPECIFIED. Relay candidates carry the relay server
// address so the signaling server can classify the relay provider.
func classifyCandidate(stats webrtc.StatsReport, candID string) *webrtcpb.ConnectionCandidate {
	cand, ok := stats[candID].(webrtc.ICECandidateStats)
	if !ok {
		return &webrtcpb.ConnectionCandidate{}
	}
	switch cand.CandidateType {
	case webrtc.ICECandidateTypeHost:
		return &webrtcpb.ConnectionCandidate{Type: webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_HOST}
	case webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypePrflx:
		return &webrtcpb.ConnectionCandidate{Type: webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_STUN}
	case webrtc.ICECandidateTypeRelay:
		return &webrtcpb.ConnectionCandidate{Type: webrtcpb.ICECandidateType_ICE_CANDIDATE_TYPE_RELAY, RelayAddress: cand.IP}
	}
	return &webrtcpb.ConnectionCandidate{}
}
