package rpc

import (
	"context"
	"strconv"
	"time"

	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/utils"
)

// When a WebRTC connection is relayed through a TURN server, pion keeps the TURN allocation
// alive for the life of the PeerConnection, refreshing permissions with the credentials baked
// in at dial time. Signaling servers commonly mint time-limited TURN credentials, so once they
// expire the TURN server rejects the refresh and a relayed connection eventually drops.
//
// The expiry is encoded in the credential itself: the username is the unix expiry timestamp
// (the TURN REST convention). maintainTURNCredentials reads it off the live PeerConnection and,
// before it lapses, re-fetches fresh credentials and applies them with an ICE restart so the
// allocation is re-established without tearing down the connection.

const (
	// turnRefreshLeadFactor is how early, as a fraction of the credential's remaining lifetime,
	// to refresh. 0.5 == halfway to expiry.
	turnRefreshLeadFactor = 0.5
	// turnRefreshMinWait avoids busy-looping on already-expired or near-expired credentials.
	turnRefreshMinWait = time.Minute
	// turnRefreshMaxWait bounds the pre-expiry refresh schedule.
	turnRefreshMaxWait = 24 * time.Hour
	// turnRefreshRecheckWait is how often to re-evaluate a connection that is not currently
	// using the relay (so we notice if ICE later fails over onto it).
	turnRefreshRecheckWait = time.Hour
)

// relaySelected reports whether the connection's currently selected ICE candidate pair is
// using a relay (TURN) local candidate. Credentials only need refreshing when the relay is
// the active path: a connection that merely has TURN in its config but is running over a
// host/srflx pair keeps a background TURN allocation alive, but its expiry is harmless noise,
// not a path to refresh.
func relaySelected(peerConn *webrtc.PeerConnection) bool {
	pair, ok := webrtcPeerConnCandPair(peerConn)
	return ok && pair.Local != nil && pair.Local.Typ == webrtc.ICECandidateTypeRelay
}

// earliestTURNCredentialExpiry returns the soonest expiry encoded in any of the live
// PeerConnection's TURN credential usernames. ok is false when there is no time-limited TURN
// credential in play (e.g. a non-relayed connection, or a non-timestamp username scheme), in
// which case there is nothing to proactively refresh.
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
			// Not a unix-timestamp credential; we can't derive an expiry from it.
			continue
		}
		expiry := time.Unix(secs, 0)
		if !found || expiry.Before(earliest) {
			earliest, found = expiry, true
		}
	}
	return earliest, found
}

// nextTURNRefreshWait computes how long to wait before the next refresh. ok is true only when
// the connection is actively relayed through a TURN server with a time-limited credential — the
// only case that needs refreshing. Otherwise it returns a recheck interval with ok=false, so the
// worker periodically re-evaluates (e.g. in case ICE later fails over onto the relay) without
// doing any work.
func nextTURNRefreshWait(peerConn *webrtc.PeerConnection) (time.Duration, bool) {
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
	wait := time.Duration(float64(remaining) * turnRefreshLeadFactor)
	if wait < turnRefreshMinWait {
		wait = turnRefreshMinWait
	}
	if wait > turnRefreshMaxWait {
		wait = turnRefreshMaxWait
	}
	return wait, true
}

// maintainTURNCredentials refreshes time-limited TURN credentials on a long-lived connection
// whose selected ICE path is a relay, before they expire. It runs until ctx is done.
// refetchICEServers fetches a fresh set of ICE servers (e.g. by re-querying the signaling
// server's OptionalWebRTCConfig), and renegotiate applies them via an ICE restart. For
// connections not actively using a relay it does no work beyond a periodic check of the selected
// candidate pair, so callers need not know in advance whether they will connect over a relay.
func maintainTURNCredentials(
	ctx context.Context,
	peerConn *webrtc.PeerConnection,
	refetchICEServers func(context.Context) ([]webrtc.ICEServer, error),
	renegotiate renegotiateFunc,
	logger utils.ZapCompatibleLogger,
) {
	for {
		wait, ok := nextTURNRefreshWait(peerConn)
		if !utils.SelectContextOrWait(ctx, wait) {
			return
		}
		// Re-check after sleeping: ok gated on the relay being the active path, which can change
		// over a long wait. Only refresh if the relay is (still) selected.
		if !ok || !relaySelected(peerConn) {
			continue
		}

		servers, err := refetchICEServers(ctx)
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
		if err := renegotiate(ctx, true); err != nil {
			logger.Warnw("turn credential refresh: ICE restart failed", "error", err)
			continue
		}
		logger.Debug("refreshed TURN credentials via ICE restart")
	}
}
