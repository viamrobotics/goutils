package rpc

import (
	"context"
	"strconv"
	"time"

	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/utils"
)

// A connection relayed through a TURN server holds a relay allocation whose time-limited credentials
// (the username is the unix expiry timestamp, per the TURN REST convention) eventually expire; once
// they do the relay path drops. There is no way to swap credentials on a live allocation, so we
// refresh by performing an ICE restart that re-gathers a fresh allocation.
//
// To avoid glare (simultaneous offers, which our webrtc fork cannot recover from — it has no
// SDP rollback), the SERVER is the sole initiator: it periodically initiates the ICE restart
// (initiateTURNRefresh). Each peer keeps its OWN credentials fresh in its config
// (refreshOwnTURNCredentials), so that when the restart re-gathers, both sides produce a relay
// allocation with valid credentials. We only do any of this while the connection is actually using
// a relay (either end of the selected pair).

const (
	// turnClientRefreshLeadFactor: a peer refreshes its config this fraction into the credential
	// lifetime, comfortably before the server initiates the restart.
	turnClientRefreshLeadFactor = 0.4
	// turnServerInitiateInterval: the server initiates an ICE restart on this fixed cadence. It
	// assumes the signaling server's ~24h TURN credential TTL and is chosen to land after a peer's
	// config refresh (~0.4 of lifetime) and well before expiry.
	turnServerInitiateInterval = 12 * time.Hour
	// turnRefreshMinWait avoids busy-looping on already-expired or near-expired credentials.
	turnRefreshMinWait = time.Minute
	// turnRefreshMaxWait bounds the pre-expiry refresh schedule.
	turnRefreshMaxWait = 24 * time.Hour
	// turnRefreshRecheckWait is how often to re-evaluate a connection that isn't currently using a
	// relay, so we notice if ICE later settles on (or fails over to) one.
	turnRefreshRecheckWait = time.Hour
)

// relaySelected reports whether the connection's currently selected ICE candidate pair uses a relay
// (TURN) candidate at either end — our own (local) or the peer's (remote).
func relaySelected(peerConn *webrtc.PeerConnection) bool {
	if peerConn == nil {
		return false
	}
	pair, ok := webrtcPeerConnCandPair(peerConn)
	if !ok || pair == nil {
		return false
	}
	return (pair.Local != nil && pair.Local.Typ == webrtc.ICECandidateTypeRelay) ||
		(pair.Remote != nil && pair.Remote.Typ == webrtc.ICECandidateTypeRelay)
}

// earliestTURNCredentialExpiry returns the soonest expiry encoded in any of the connection's TURN
// credential usernames. ok is false when there is no time-limited TURN credential (e.g. a
// non-timestamp username scheme, or — for an answerer — no TURN servers configured at all).
func earliestTURNCredentialExpiry(peerConn *webrtc.PeerConnection) (time.Time, bool) {
	if peerConn == nil {
		return time.Time{}, false
	}
	var earliest time.Time
	found := false
	for _, server := range peerConn.GetConfiguration().ICEServers {
		if server.Username == "" {
			continue
		}
		secs, err := strconv.ParseInt(server.Username, 10, 64)
		if err != nil {
			continue
		}
		expiry := time.Unix(secs, 0)
		if !found || expiry.Before(earliest) {
			earliest, found = expiry, true
		}
	}
	return earliest, found
}

// refreshOwnTURNCredentials keeps this peer's ICE config stocked with fresh credentials while the
// connection is relayed, so that when the server initiates an ICE restart this peer re-gathers a
// relay allocation with valid credentials. It is config-only: it never initiates a renegotiation
// (only the server does, to avoid glare). refetch fetches a fresh ICE-server set (e.g. by
// re-querying the signaling server's OptionalWebRTCConfig). Runs until ctx is done.
func refreshOwnTURNCredentials(
	ctx context.Context,
	peerConn *webrtc.PeerConnection,
	refetch func(context.Context) ([]webrtc.ICEServer, error),
	logger utils.ZapCompatibleLogger,
) {
	for {
		wait, relayed := nextOwnRefreshWait(peerConn)
		if !utils.SelectContextOrWait(ctx, wait) {
			return
		}
		if !relayed || !relaySelected(peerConn) {
			continue
		}
		servers, err := refetch(ctx)
		if err != nil {
			logger.Warnw("turn credential refresh: failed to refetch ICE servers", "error", err)
			continue
		}
		config := peerConn.GetConfiguration()
		config.ICEServers = servers
		if err := peerConn.SetConfiguration(config); err != nil {
			logger.Warnw("turn credential refresh: failed to apply refreshed ICE servers", "error", err)
			continue
		}
		logger.Debug("refreshed own TURN credentials in config ahead of next ICE restart")
	}
}

// nextOwnRefreshWait computes how long to wait before refreshing our own credentials. ok is true
// only when the connection is actively relayed with a time-limited credential; otherwise it returns
// a recheck interval.
func nextOwnRefreshWait(peerConn *webrtc.PeerConnection) (time.Duration, bool) {
	if !relaySelected(peerConn) {
		return turnRefreshRecheckWait, false
	}
	expiry, ok := earliestTURNCredentialExpiry(peerConn)
	if !ok {
		return turnRefreshRecheckWait, false
	}
	remaining := time.Until(expiry)
	if remaining <= 0 {
		return turnRefreshMinWait, true
	}
	wait := time.Duration(float64(remaining) * turnClientRefreshLeadFactor)
	if wait < turnRefreshMinWait {
		wait = turnRefreshMinWait
	}
	if wait > turnRefreshMaxWait {
		wait = turnRefreshMaxWait
	}
	return wait, true
}

// initiateTURNRefresh periodically initiates an ICE restart so a relayed connection re-establishes
// its TURN allocation with fresh credentials. The server is the sole initiator, which avoids glare.
// It only acts while the connection is actually using a relay. If this peer itself holds
// time-limited credentials (e.g. an answerer behind a proxy), it refreshes its own config first so
// its re-gather is fresh too. Runs until ctx is done.
func initiateTURNRefresh(
	ctx context.Context,
	peerConn *webrtc.PeerConnection,
	refetch func(context.Context) ([]webrtc.ICEServer, error),
	renegotiate renegotiateFunc,
	logger utils.ZapCompatibleLogger,
) {
	for {
		if !utils.SelectContextOrWait(ctx, turnServerInitiateInterval) {
			return
		}
		if !relaySelected(peerConn) {
			continue
		}
		// If we hold our own time-limited credentials, refresh them before re-gathering.
		if _, ok := earliestTURNCredentialExpiry(peerConn); ok {
			if servers, err := refetch(ctx); err != nil {
				logger.Warnw("turn credential refresh: server failed to refetch own ICE servers", "error", err)
			} else {
				config := peerConn.GetConfiguration()
				config.ICEServers = servers
				if err := peerConn.SetConfiguration(config); err != nil {
					logger.Warnw("turn credential refresh: server failed to apply refreshed ICE servers", "error", err)
				}
			}
		}
		if err := renegotiate(ctx, true); err != nil {
			logger.Warnw("turn credential refresh: server ICE restart failed", "error", err)
			continue
		}
		logger.Debug("initiated ICE restart to refresh relayed TURN credentials")
	}
}
