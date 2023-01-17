package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/edaniels/golog"
	"github.com/edaniels/zeroconf"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// Dial attempts to make the most convenient connection to the given address. It attempts to connect
// via WebRTC if a signaling server is detected or provided. Otherwise it attempts to connect directly.
// TODO(GOUT-7): figure out decent way to handle reconnect on connection termination.
func Dial(ctx context.Context, address string, logger golog.Logger, opts ...DialOption) (ClientConn, error) {
	var dOpts dialOptions
	for _, opt := range opts {
		opt.apply(&dOpts)
	}

	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	return dialInner(ctx, address, logger, &dOpts)
}

func dialInner(
	ctx context.Context,
	address string,
	logger golog.Logger,
	dOpts *dialOptions,
) (ClientConn, error) {
	if address == "" {
		return nil, errors.New("address empty")
	}

	conn, cached, err := dialFunc(
		ctx,
		"multi",
		address,
		buildKeyExtra(dOpts),
		func() (ClientConn, error) {
			if dOpts.debug {
				logger.Debugw("starting to dial", "address", address)
			}

			if dOpts.authEntity == "" {
				if dOpts.externalAuthAddr == "" {
					// if we are not doing external auth, then the entity is assumed to be the actual address.
					if dOpts.debug {
						logger.Debugw("auth entity empty; setting to address", "address", address)
					}
					dOpts.authEntity = address
				} else {
					// otherwise it's the external auth address.
					if dOpts.debug {
						logger.Debugw("auth entity empty; setting to external auth address", "address", dOpts.externalAuthAddr)
					}
					dOpts.authEntity = dOpts.externalAuthAddr
				}
			}

			conn, _, err := dial(ctx, address, address, logger, dOpts, true)
			return conn, err
		})
	if err != nil {
		return nil, err
	}
	if cached {
		if dOpts.debug {
			logger.Debugw("connected (cached)", "address", address)
		}
	}
	return conn, nil
}

// ErrConnectionOptionsExhausted is returned in cases where the given options have all been used up with
// no way to connect on any of them.
var ErrConnectionOptionsExhausted = errors.New("exhausted all connection options with no way to connect")

func dial(
	ctx context.Context,
	address string,
	originalAddress string,
	logger golog.Logger,
	dOpts *dialOptions,
	tryLocal bool,
) (ClientConn, bool, error) {
	var isJustDomain bool
	if strings.HasPrefix(address, "unix://") {
		dOpts.mdnsOptions.Disable = true
		dOpts.webrtcOpts.Disable = true
		dOpts.insecure = true
		dOpts.disableDirect = false
	} else {
		if strings.ContainsRune(address, ':') {
			isJustDomain = false
		} else {
			isJustDomain = net.ParseIP(address) == nil
		}
	}

	if !dOpts.mdnsOptions.Disable && tryLocal && isJustDomain {
		conn, cached, err := dialMulticastDNS(ctx, address, logger, dOpts)
		if err != nil || conn != nil {
			return conn, cached, err
		}
	}

	if !dOpts.webrtcOpts.Disable {
		signalingAddress := dOpts.webrtcOpts.SignalingServerAddress
		if signalingAddress == "" || dOpts.webrtcOpts.AllowAutoDetectAuthOptions {
			if signalingAddress == "" {
				// try WebRTC at same address
				signalingAddress = address
			}
			target, port, err := getWebRTCTargetFromAddressWithDefaults(signalingAddress)
			if err != nil {
				return nil, false, err
			}
			fixupWebRTCOptions(dOpts, target, port)

			// When connecting to an external signaler for WebRTC, we assume we can use the external auth's material.
			// This path is also called by an mdns direct connection and ignores that case.
			// This will skip all Authenticate/AuthenticateTo calls for the signaler.
			if !dOpts.usingMDNS && dOpts.authMaterial == "" && dOpts.webrtcOpts.SignalingExternalAuthAuthMaterial != "" {
				logger.Debug("using signaling's external auth as auth material")
				dOpts.authMaterial = dOpts.webrtcOpts.SignalingExternalAuthAuthMaterial
				dOpts.creds = Credentials{}
			}
		}

		if dOpts.debug {
			logger.Debugw(
				"trying WebRTC",
				"signaling_server", dOpts.webrtcOpts.SignalingServerAddress,
				"host", originalAddress,
			)
		}

		conn, cached, err := dialFunc(
			ctx,
			"webrtc",
			fmt.Sprintf("%s->%s", dOpts.webrtcOpts.SignalingServerAddress, originalAddress),
			buildKeyExtra(dOpts),
			func() (ClientConn, error) {
				return dialWebRTC(
					ctx,
					dOpts.webrtcOpts.SignalingServerAddress,
					originalAddress,
					dOpts,
					logger,
				)
			})
		if err == nil {
			if !cached {
				logger.Debug("connected via WebRTC")
			} else if dOpts.debug {
				logger.Debug("connected via WebRTC (cached)")
			}
			return conn, cached, nil
		}
		if !errors.Is(err, ErrNoWebRTCSignaler) {
			return nil, false, err
		}
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
	}

	if dOpts.disableDirect {
		return nil, false, ErrConnectionOptionsExhausted
	}
	if dOpts.debug {
		logger.Debugw("trying direct", "address", address)
	}
	conn, cached, err := dialDirectGRPC(ctx, address, dOpts, logger)
	if err != nil {
		return nil, false, err
	}
	if !cached {
		logger.Debugw("connected via gRPC", "address", address)
	} else if dOpts.debug {
		logger.Debugw("connected via gRPC (cached)", "address", address)
	}
	return conn, cached, nil
}

func dialMulticastDNS(
	ctx context.Context,
	address string,
	logger golog.Logger,
	dOpts *dialOptions,
) (ClientConn, bool, error) {
	candidates := []string{address, strings.ReplaceAll(address, ".", "-")}
	candidateLookup := func(ctx context.Context, candidates []string) (*zeroconf.ServiceEntry, error) {
		resolver, err := zeroconf.NewResolver(zeroconf.SelectIPRecordType(zeroconf.IPv4))
		if err != nil {
			return nil, err
		}
		defer resolver.Shutdown()
		for _, candidate := range candidates {
			entries := make(chan *zeroconf.ServiceEntry)
			lookupCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
			defer cancel()
			if err := resolver.Lookup(lookupCtx, candidate, "_rpc._tcp", "local.", entries); err != nil {
				logger.Errorw("error performing mDNS query", "error", err)
				return nil, err
			}
			// entries gets closed after lookupCtx expires or there is a real entry
			entry := <-entries
			if entry != nil {
				return entry, nil
			}
		}
		return nil, ctx.Err()
	}
	// lookup for candidates for backward compatibility
	entry, err := candidateLookup(ctx, candidates)
	if err != nil || entry == nil {
		return nil, false, err
	}
	var hasGRPC, hasWebRTC bool
	for _, field := range entry.Text {
		// mdns service may advertise TXT field following https://datatracker.ietf.org/doc/html/rfc1464 (ex grpc=)
		if strings.Contains(field, "grpc") {
			hasGRPC = true
		}
		if strings.Contains(field, "webrtc") {
			hasWebRTC = true
		}
	}

	// IPv6 with scope does not work with grpc-go which we would want here.
	if !(hasGRPC || hasWebRTC) || len(entry.AddrIPv4) == 0 {
		return nil, false, nil
	}

	localAddress := fmt.Sprintf("%s:%d", entry.AddrIPv4[0], entry.Port)
	if dOpts.debug {
		logger.Debugw("found address via mDNS", "address", localAddress)
	}

	dOptsCopy := *dOpts

	// Let downstream calls know when mdns was used. This is helpful to inform
	// when determining if we want to use the external auth credentials for the signaling
	// in cases where the external signaling is the same as the external auth. For mdns
	// this isn't the case.
	dOptsCopy.usingMDNS = true

	if dOptsCopy.mdnsOptions.RemoveAuthCredentials {
		dOptsCopy.creds = Credentials{}
		dOptsCopy.authEntity = ""
		dOptsCopy.externalAuthToEntity = ""
		dOptsCopy.externalAuthMaterial = ""
	}

	if hasWebRTC {
		fixupWebRTCOptions(&dOptsCopy, entry.AddrIPv4[0].String(), uint16(entry.Port))
		if dOptsCopy.mdnsOptions.RemoveAuthCredentials {
			dOptsCopy.webrtcOpts.SignalingAuthEntity = ""
			dOptsCopy.webrtcOpts.SignalingCreds = Credentials{}
			dOptsCopy.webrtcOpts.SignalingExternalAuthAuthMaterial = ""
		}
	} else {
		dOptsCopy.webrtcOpts.Disable = true
	}
	var tlsConfig *tls.Config
	if dOptsCopy.tlsConfig == nil {
		tlsConfig = newDefaultTLSConfig()
	} else {
		tlsConfig = dOptsCopy.tlsConfig.Clone()
	}
	tlsConfig.ServerName = address
	dOptsCopy.tlsConfig = tlsConfig

	conn, cached, err := dial(ctx, localAddress, address, logger, &dOptsCopy, false)
	if err == nil {
		if !cached {
			logger.Debugw("connected via mDNS", "address", localAddress)
		} else if dOptsCopy.debug {
			logger.Debugw("connected via mDNS (cached)", "address", localAddress)
		}
		return conn, cached, nil
	}
	return nil, false, err
}

// fixupWebRTCOptions sets sensible and secure settings for WebRTC dial options based on
// auto detection / connection attempts as well as what settings are not set and can be interpreted
// from non WebRTC dial options (e.g. credentials becoming signaling credentials).
func fixupWebRTCOptions(dOpts *dialOptions, target string, port uint16) {
	dOpts.webrtcOpts.SignalingServerAddress = fmt.Sprintf("%s:%d", target, port)

	if !dOpts.webrtcOptsSet {
		dOpts.webrtcOpts.SignalingInsecure = dOpts.insecure
		dOpts.webrtcOpts.SignalingExternalAuthInsecure = dOpts.externalAuthInsecure
	}

	if dOpts.webrtcOpts.SignalingExternalAuthAddress == "" {
		dOpts.webrtcOpts.SignalingExternalAuthAddress = dOpts.externalAuthAddr
	}
	if dOpts.webrtcOpts.SignalingExternalAuthToEntity == "" {
		dOpts.webrtcOpts.SignalingExternalAuthToEntity = dOpts.externalAuthToEntity
	}
	if dOpts.webrtcOpts.SignalingExternalAuthAuthMaterial == "" {
		dOpts.webrtcOpts.SignalingExternalAuthAuthMaterial = dOpts.externalAuthMaterial
	}

	// It's always okay to pass over entity and credentials since next section
	// will assume secure settings based on public internet or not.
	// The security considerations are as follows:
	// 1. from mDNS - follows insecure downgrade rules and server name TLS check
	// stays in tact, so we are transferring credentials to the same host or
	// user says they do not care.
	// 2. from trying WebRTC when signaling address not explicitly set - follows
	// insecure downgrade rules and host/target stays in tact, so we are transferring
	// credentials to the same host or user says they do not care.
	// 3. form user explicitly allowing this.
	if dOpts.webrtcOpts.SignalingAuthEntity == "" {
		dOpts.webrtcOpts.SignalingAuthEntity = dOpts.authEntity
	}
	if dOpts.webrtcOpts.SignalingCreds.Type == "" {
		dOpts.webrtcOpts.SignalingCreds = dOpts.creds
	}
}

func getWebRTCTargetFromAddressWithDefaults(signalingAddress string) (target string, port uint16, err error) {
	if strings.Contains(signalingAddress, ":") {
		host, portStr, err := net.SplitHostPort(signalingAddress)
		if err != nil {
			return "", 0, err
		}
		if strings.Contains(host, ":") {
			host = fmt.Sprintf("[%s]", host)
		}
		target = host
		portParsed, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return "", 0, err
		}
		port = uint16(portParsed)
	} else {
		target = signalingAddress
		port = 443
	}

	return target, port, nil
}
