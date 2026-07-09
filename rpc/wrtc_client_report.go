package rpc

import (
	"context"
	"net"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/viamrobotics/webrtc/v3"
	"google.golang.org/grpc/metadata"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// dialDurationMS returns milliseconds elapsed since start.
func dialDurationMS(start time.Time) uint32 {
	return uint32(time.Since(start).Milliseconds())
}

// viamSDKModule is the module path of the Viam RDK, which houses the Go SDK.
const viamSDKModule = "go.viam.com/rdk"

// sdkVersion returns the version of the Viam SDK module in the running binary.
func sdkVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if bi.Main.Path == viamSDKModule {
		return bi.Main.Version
	}
	for _, dep := range bi.Deps {
		if dep.Path == viamSDKModule {
			return dep.Version
		}
	}
	return ""
}

// pendingReport is one WebRTC dial attempt's would-be connection-metadata report, held together
// with the signaling connection it must be sent over until the parallel dial resolves.
type pendingReport struct {
	req  *webrtcpb.ReportConnectionMetadataRequest
	conn ClientConn
}

// dialReportCollector accumulates the per-attempt reports of a single logical dial (which races
// mDNS and cloud attempts) and, on flush, sends exactly one: the furthest-progressed attempt then
// closes every held signaling connection.
type dialReportCollector struct {
	ctx     context.Context
	host    string
	logger  utils.ZapCompatibleLogger
	mu      sync.Mutex
	reports []pendingReport
}

func (c *dialReportCollector) add(r pendingReport) {
	c.mu.Lock()
	c.reports = append(c.reports, r)
	c.mu.Unlock()
}

// flush sends a single connection report for a logical dial and closes every held signaling connection.
// dialErr is the logical dial's outcome: when the dial succeeded, only a READY report is truthful — a
// non-READY "best" means the successful attempt produced no report of its own (e.g. a cached connection
// was reused, so no dial actually happened), so it is suppressed rather than counting a failure against
// a dial that succeeded.
func (c *dialReportCollector) flush(dialErr error) {
	c.mu.Lock()
	reports := c.reports
	c.reports = nil
	c.mu.Unlock()

	// Release the held signaling connections regardless of what, if anything, we report.
	defer func() {
		for _, r := range reports {
			utils.UncheckedError(r.conn.Close())
		}
	}()

	if len(reports) == 0 {
		return
	}
	best := furthestPendingReport(reports)
	if dialErr == nil && best.req.GetReachedStage() != webrtcpb.DialStage_DIAL_STAGE_READY {
		return
	}
	c.send(best)
}

// send delivers one report to the signaling server over its own connection, best-effort: it stamps
// the SDK type/version and rpc-host, logs (never returns) any error, and detaches from the dial
// context so a cancelled/timed-out dial still reports, with a 5s bound.
func (c *dialReportCollector) send(report pendingReport) {
	report.req.SdkType = webrtcpb.SDKType_SDK_TYPE_GO
	report.req.SdkVersion = sdkVersion()

	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), 5*time.Second)
	defer cancel()
	reportCtx = metadata.NewOutgoingContext(reportCtx, metadata.New(map[string]string{RPCHostMetadataField: c.host}))

	client := webrtcpb.NewSignalingServiceClient(report.conn)
	if _, err := client.ReportConnectionMetadata(reportCtx, report.req); err != nil {
		c.logger.Debugw("failed to report connection metadata", "reached_stage", report.req.GetReachedStage(), "err", err)
	}
}

// furthestPendingReport returns the attempt that progressed the furthest: READY has the highest stage
// and always wins; among failures the one that reached the latest stage.
func furthestPendingReport(reports []pendingReport) pendingReport {
	var best pendingReport
	for i, r := range reports {
		if i == 0 || r.req.GetReachedStage() > best.req.GetReachedStage() {
			best = r
		}
	}
	return best
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
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if slices.Contains(viamCloudSignalingHosts, strings.ToLower(host)) {
		return webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_CLOUD_SIGNALED
	}
	return webrtcpb.ConnectionSignalingPath_CONNECTION_SIGNALING_PATH_LOCAL
}

// classifyConnection inspects the nominated ICE candidate pair and classifies each side into a
// ConnectionCandidate. Both are UNSPECIFIED when peerConn is nil (a failed dial) or no succeeded,
// nominated pair exists.
func classifyConnection(peerConn *webrtc.PeerConnection) (local, remote *webrtcpb.ConnectionCandidate) {
	if peerConn == nil {
		return &webrtcpb.ConnectionCandidate{}, &webrtcpb.ConnectionCandidate{}
	}
	stats := peerConn.GetStats()
	var localCandID, remoteCandID string
	for _, stat := range stats {
		pair, ok := stat.(webrtc.ICECandidatePairStats)
		if !ok || !pair.Nominated || pair.State != webrtc.StatsICECandidatePairStateSucceeded {
			continue
		}
		localCandID, remoteCandID = pair.LocalCandidateID, pair.RemoteCandidateID
		break
	}

	return classifyCandidate(stats, localCandID), classifyCandidate(stats, remoteCandID)
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
