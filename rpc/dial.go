package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
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

	return dialInner(ctx, address, logger, dOpts)
}

func dialInner(
	ctx context.Context,
	address string,
	logger golog.Logger,
	dOpts dialOptions,
) (ClientConn, error) {
	if address == "" {
		return nil, errors.New("address empty")
	}

	conn, cached, err := dialFunc(
		ctx,
		"multi",
		address,
		dOpts.cacheKey(),
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

// dialResult contains information about a concurrent dial attempt.
type dialResult struct {
	// a successfully established connection
	conn ClientConn
	// whether or not the connection is reused
	cached bool
	// connection errors
	err error
	// whether we should skip dialing gRPC directly as a fallback
	skipDirect bool
}

func dial(
	ctx context.Context,
	address string,
	originalAddress string,
	logger golog.Logger,
	dOpts dialOptions,
	tryLocal bool,
) (ClientConn, bool, error) {
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}

	var isJustDomain bool
	switch {
	case strings.HasPrefix(address, "unix://"):
		dOpts.mdnsOptions.Disable = true
		dOpts.webrtcOpts.Disable = true
		dOpts.insecure = true
		dOpts.disableDirect = false
	case strings.ContainsRune(address, ':'):
		isJustDomain = false
	default:
		isJustDomain = net.ParseIP(address) == nil
	}

	// We make concurrent dial attempts via mDNS and WebRTC, taking the first connection
	// that succeeds. We then cancel the slower connection and wait for its coroutine to
	// complete. If the slower connection succeeds before it can be cancelled then we
	// explicitly close it to prevent a memory leak.
	var (
		wg                          sync.WaitGroup
		dialCh                      = make(chan dialResult)
		ctxParallel, cancelParallel = context.WithCancelCause(ctx)
	)
	defer cancelParallel(nil)
	if !dOpts.mdnsOptions.Disable && tryLocal && isJustDomain {
		wg.Add(1)
		go func(dOpts dialOptions) {
			defer wg.Done()
			conn, cached, err := dialMulticastDNS(ctxParallel, address, logger, dOpts)
			switch {
			case err != nil:
				dialCh <- dialResult{err: err}
			case conn != nil:
				// TODO(RSDK-6490): investigate why we can get a `nil` connection that is
				// not accompanied with an error.
				dialCh <- dialResult{conn: conn, cached: cached}
			default:
				dialCh <- dialResult{err: errors.New("no connection")}
			}
		}(dOpts)
	}

	if !dOpts.webrtcOpts.Disable {
		wg.Add(1)
		go func(dOpts dialOptions) {
			defer wg.Done()
			signalingAddress := dOpts.webrtcOpts.SignalingServerAddress
			if signalingAddress == "" || dOpts.webrtcOpts.AllowAutoDetectAuthOptions {
				if signalingAddress == "" {
					// try WebRTC at same address
					signalingAddress = address
				}
				target, port, err := getWebRTCTargetFromAddressWithDefaults(signalingAddress)
				if err != nil {
					// TODO(RSDK-6493): Investigate if we must `skipDirect` here.
					dialCh <- dialResult{err: err, skipDirect: true}
					return
				}
				dOpts.fixupWebRTCOptions(target, port)

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
				ctxParallel,
				"webrtc",
				fmt.Sprintf("%s->%s", dOpts.webrtcOpts.SignalingServerAddress, originalAddress),
				dOpts.cacheKey(),
				func() (ClientConn, error) {
					return dialWebRTC(
						ctxParallel,
						dOpts.webrtcOpts.SignalingServerAddress,
						originalAddress,
						dOpts,
						logger,
					)
				})

			switch {
			case err == nil:
				if dOpts.debug {
					logger.Debugw("connected via WebRTC",
						"address", address,
						"cached", cached,
						"using mDNS", dOpts.usingMDNS,
					)
				}
				dialCh <- dialResult{conn: conn, cached: cached}
			case !errors.Is(err, ErrNoWebRTCSignaler):
				// TODO(RSDK-6493): Investigate if we must `skipDirect` here.
				dialCh <- dialResult{err: err, skipDirect: true}
			case ctxParallel.Err() != nil:
				dialCh <- dialResult{err: ctxParallel.Err(), skipDirect: true}
			default:
				dialCh <- dialResult{err: err}
			}
		}(dOpts)
	}

	// Make sure the slower connection attempt is fully cancelled, or if the attempt succeeded,
	// close the slower connection.
	go func() {
		wg.Wait()
		close(dialCh)
	}()

	var (
		conn   ClientConn
		cached bool
		err    error
	)
	for result := range dialCh {
		switch {
		case result.err == nil && result.conn != nil:
			if conn != nil {
				errClose := conn.Close()
				if errClose != nil {
					logger.Warnw("unable to close redundant connection", "error", err)
				}
			}
			conn, cached = result.conn, result.cached
			cancelParallel(errors.New("using another established connection"))
		case result.err != nil && result.skipDirect:
			err = result.err
		}
	}

	if conn != nil {
		return conn, cached, nil
	}
	if err != nil {
		return nil, false, err
	}

	if dOpts.disableDirect {
		return nil, false, ErrConnectionOptionsExhausted
	}
	if dOpts.debug {
		logger.Debugw("trying direct", "address", address)
	}
	conn, cached, err = dialDirectGRPC(ctx, address, dOpts, logger)
	if err != nil {
		return nil, false, err
	}
	if dOpts.debug {
		logger.Debugw("connected via gRPC",
			"address", address,
			"cached", cached,
			"using mDNS", dOpts.usingMDNS,
		)
	}
	return conn, cached, nil
}

func dialMulticastDNS(
	ctx context.Context,
	address string,
	logger golog.Logger,
	dOpts dialOptions,
) (ClientConn, bool, error) {
	candidates := []string{address, strings.ReplaceAll(address, ".", "-")}
	candidateLookup := func(ctx context.Context, candidates []string) (*zeroconf.ServiceEntry, error) {
		resolver, err := zeroconf.NewResolver(logger, zeroconf.SelectIPRecordType(zeroconf.IPv4))
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
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			// entries gets closed after lookupCtx expires or there is a real entry
			case entry := <-entries:
				if entry != nil {
					return entry, nil
				}
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

	// Let downstream calls know when mdns was used. This is helpful to inform
	// when determining if we want to use the external auth credentials for the signaling
	// in cases where the external signaling is the same as the external auth. For mdns
	// this isn't the case.
	dOpts.usingMDNS = true

	if dOpts.mdnsOptions.RemoveAuthCredentials {
		dOpts.creds = Credentials{}
		dOpts.authEntity = ""
		dOpts.externalAuthToEntity = ""
		dOpts.externalAuthMaterial = ""
	}

	if hasWebRTC {
		dOpts.fixupWebRTCOptions(entry.AddrIPv4[0].String(), uint16(entry.Port))
		if dOpts.mdnsOptions.RemoveAuthCredentials {
			dOpts.webrtcOpts.SignalingAuthEntity = ""
			dOpts.webrtcOpts.SignalingCreds = Credentials{}
			dOpts.webrtcOpts.SignalingExternalAuthAuthMaterial = ""
		}
	} else {
		dOpts.webrtcOpts.Disable = true
	}
	var tlsConfig *tls.Config
	if dOpts.tlsConfig == nil {
		tlsConfig = newDefaultTLSConfig()
	} else {
		tlsConfig = dOpts.tlsConfig.Clone()
	}
	tlsConfig.ServerName = address
	dOpts.tlsConfig = tlsConfig

	conn, cached, err := dial(ctx, localAddress, address, logger, dOpts, false)
	if err == nil {
		return conn, cached, nil
	}
	return nil, false, err
}

// fixupWebRTCOptions sets sensible and secure settings for WebRTC dial options based on
// auto detection / connection attempts as well as what settings are not set and can be interpreted
// from non WebRTC dial options (e.g. credentials becoming signaling credentials).
func (dOpts *dialOptions) fixupWebRTCOptions(target string, port uint16) {
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
