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

// Dial attempts to makeee the most convenient connection to the given address. It attempts to connect
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
	if strings.ContainsRune(address, ':') {
		isJustDomain = false
	} else {
		isJustDomain = net.ParseIP(address) == nil
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

			var target string
			var port uint16
			if strings.Contains(signalingAddress, ":") {
				host, portStr, err := net.SplitHostPort(signalingAddress)
				if err != nil {
					return nil, false, err
				}
				if strings.Contains(host, ":") {
					host = fmt.Sprintf("[%s]", host)
				}
				target = host
				portParsed, err := strconv.ParseUint(portStr, 10, 16)
				if err != nil {
					return nil, false, err
				}
				port = uint16(portParsed)
			} else {
				target = signalingAddress
				port = 443
			}
			fixupWebRTCOptions(dOpts, target, port)
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
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIPRecordType(zeroconf.IPv4))
	if err != nil {
		return nil, false, err
	}
	defer resolver.Shutdown()
	entries := make(chan *zeroconf.ServiceEntry)
	lookupCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	if err := resolver.Lookup(lookupCtx, address, "_rpc._tcp", "local.", entries); err != nil {
		logger.Errorw("error performing mDNS query", "error", err)
		return nil, false, nil
	}

	// entries gets closed after lookupCtx expires or there is a real entry
	entry := <-entries
	if entry == nil {
		return nil, false, ctx.Err()
	}
	var hasGRPC, hasWebRTC bool
	for _, field := range entry.Text {
		if field == "grpc" {
			hasGRPC = true
		}
		if field == "webrtc" {
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
	if dOptsCopy.mdnsOptions.RemoveAuthCredentials {
		dOptsCopy.creds = Credentials{}
		dOptsCopy.authEntity = ""
		dOptsCopy.externalAuthToEntity = ""
	}

	if hasWebRTC {
		fixupWebRTCOptions(&dOptsCopy, entry.AddrIPv4[0].String(), uint16(entry.Port))
		if dOptsCopy.mdnsOptions.RemoveAuthCredentials {
			dOptsCopy.webrtcOpts.SignalingAuthEntity = ""
			dOptsCopy.webrtcOpts.SignalingCreds = Credentials{}
		}
	} else {
		dOptsCopy.webrtcOpts.Disable = true
	}
	var tlsConfig *tls.Config
	if dOptsCopy.tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
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
