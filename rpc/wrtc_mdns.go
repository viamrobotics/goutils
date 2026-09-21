package rpc

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/pion/mdns"
	"github.com/pkg/errors"
	"github.com/viamrobotics/ice/v2"
	"github.com/viamrobotics/webrtc/v3"
	"go.uber.org/multierr"
	"golang.org/x/net/ipv4"

	"go.viam.com/utils"
)

// mdnsCandidateResolveTimeout bounds how long a remote mDNS (<uuid>.local) ICE candidate is
// queried for before it is dropped. Browsers obfuscate their host candidates this way, and a
// browser on another network can never be resolved. Left to ICE, the query would repeat once a
// second on every interface for the life of the peer connection.
const mdnsCandidateResolveTimeout = 5 * time.Second

// mdnsCandidateAddressField is the index of the connection address in a candidate attribute
// ("candidate:<foundation> <component> <transport> <priority> <address> <port> typ ...").
const mdnsCandidateAddressField = 4

// iceCandidateAdder is the part of a PeerConnection used to add remote candidates.
type iceCandidateAdder interface {
	AddICECandidate(webrtc.ICECandidateInit) error
}

// mdnsCandidateAddress returns the .local hostname of a host candidate, or false if the
// candidate does not refer to an mDNS name.
func mdnsCandidateAddress(candidate string) (string, bool) {
	c, err := ice.UnmarshalCandidate(strings.TrimPrefix(candidate, "candidate:"))
	if err != nil || c.Type() != ice.CandidateTypeHost || !strings.HasSuffix(c.Address(), ".local") {
		return "", false
	}
	return c.Address(), true
}

// addRemoteICECandidate adds a remote candidate to pc. Candidates naming an mDNS host are resolved
// in the background, bounded by mdnsCandidateResolveTimeout and ctx, and added once resolved;
// unresolvable ones are dropped.
func addRemoteICECandidate(
	ctx context.Context,
	pc iceCandidateAdder,
	cand webrtc.ICECandidateInit,
	logger utils.ZapCompatibleLogger,
) error {
	return addRemoteICECandidateWithTimeout(ctx, pc, cand, mdnsCandidateResolveTimeout, logger)
}

func addRemoteICECandidateWithTimeout(
	ctx context.Context,
	pc iceCandidateAdder,
	cand webrtc.ICECandidateInit,
	timeout time.Duration,
	logger utils.ZapCompatibleLogger,
) error {
	name, ok := mdnsCandidateAddress(cand.Candidate)
	if !ok {
		return pc.AddICECandidate(cand)
	}
	utils.PanicCapturingGo(func() {
		resolved, err := resolveMDNSCandidate(ctx, cand, name, timeout)
		if err != nil {
			logger.Debugw("dropping unresolvable mDNS ICE candidate", "name", name, "error", err)
			return
		}
		if err := pc.AddICECandidate(resolved); err != nil {
			logger.Debugw("error adding resolved mDNS ICE candidate", "name", name, "error", err)
		}
	})
	return nil
}

// addRemoteICECandidates adds each candidate as addRemoteICECandidate does, logging rather
// than returning errors for the synchronous ones.
func addRemoteICECandidates(
	ctx context.Context,
	pc iceCandidateAdder,
	cands []webrtc.ICECandidateInit,
	logger utils.ZapCompatibleLogger,
) {
	for _, cand := range cands {
		if err := addRemoteICECandidate(ctx, pc, cand, logger); err != nil {
			logger.Warnw("Error adding candidate", "err", err)
		}
	}
}

// resolveMDNSCandidate returns cand with the mDNS name in its address field replaced by the
// IPv4 address it resolves to.
func resolveMDNSCandidate(
	ctx context.Context,
	cand webrtc.ICECandidateInit,
	name string,
	timeout time.Duration,
) (webrtc.ICECandidateInit, error) {
	fields := strings.Fields(cand.Candidate)
	if len(fields) <= mdnsCandidateAddressField || fields[mdnsCandidateAddressField] != name {
		return cand, errors.Errorf("malformed mDNS candidate %q", cand.Candidate)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ip, err := queryMDNSAddress(ctx, name)
	if err != nil {
		return cand, err
	}
	fields[mdnsCandidateAddressField] = ip.String()
	resolved := cand
	resolved.Candidate = strings.Join(fields, " ")
	return resolved, nil
}

// queryMDNSAddress resolves an mDNS name to an IPv4 address using a short-lived querier that is
// torn down as soon as the query completes or ctx ends.
func queryMDNSAddress(ctx context.Context, name string) (net.IP, error) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(224, 0, 0, 0), Port: 5353})
	if err != nil {
		return nil, err
	}
	conn, err := mdns.Server(ipv4.NewPacketConn(udpConn), &mdns.Config{IncludeLoopback: true})
	if err != nil {
		return nil, multierr.Combine(err, udpConn.Close())
	}
	defer func() { utils.UncheckedError(conn.Close()) }()
	_, src, err := conn.Query(ctx, name)
	if err != nil {
		return nil, err
	}
	switch addr := src.(type) {
	case *net.IPAddr:
		return addr.IP, nil
	case *net.UDPAddr:
		return addr.IP, nil
	default:
		return nil, errors.Errorf("unexpected mDNS answer address %T", src)
	}
}

// stripMDNSCandidatesFromSDP removes candidate attributes that name an mDNS host from sdp and
// returns them so they can be resolved and added separately.
func stripMDNSCandidatesFromSDP(sdp string) (string, []webrtc.ICECandidateInit) {
	var stripped []webrtc.ICECandidateInit
	lines := strings.SplitAfter(sdp, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		attr := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(attr, "a=candidate:") {
			candidate := strings.TrimPrefix(attr, "a=")
			if _, ok := mdnsCandidateAddress(candidate); ok {
				stripped = append(stripped, webrtc.ICECandidateInit{Candidate: candidate})
				continue
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, ""), stripped
}
