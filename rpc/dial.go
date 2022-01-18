package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/edaniels/golog"
)

// Dial attempts to make the most convenient connection to the given address. It attempts to connect
// via WebRTC if a signaling server is detected or provided. Otherwise it attempts to connect directly.
// TODO(https://github.com/viamrobotics/goutils/issues/19): figure out decent way to handle reconnect on connection termination.
func Dial(ctx context.Context, address string, logger golog.Logger, opts ...DialOption) (ClientConn, error) {
	var dOpts dialOptions
	for _, opt := range opts {
		opt.apply(&dOpts)
	}

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

	if dOpts.webrtcOpts.SignalingServerAddress == "" {
		srvTimeoutCtx, srvTimeoutCtxCancel := context.WithTimeout(ctx, 5*time.Second)
		defer srvTimeoutCtxCancel()
		if target, port, err := lookupSRV(srvTimeoutCtx, address); err == nil {
			if dOpts.debug {
				logger.Debugw("found SRV record for address", "target", target, "port", port)
			}
			dOpts.webrtcOpts.SignalingInsecure = port != 443
			dOpts.webrtcOpts.SignalingServerAddress = fmt.Sprintf("%s:%d", target, port)
			dOpts.webrtcOpts.SignalingAuthEntity = dOpts.authEntity
			dOpts.webrtcOpts.SignalingCreds = dOpts.creds
			dOpts.webrtcOpts.SignalingExternalAuthAddress = ""
			dOpts.webrtcOpts.SignalingExternalAuthToEntity = ""
			dOpts.webrtcOpts.SignalingExternalAuthInsecure = false
		} else if srvTimeoutCtx.Err() != nil && !errors.Is(srvTimeoutCtx.Err(), context.DeadlineExceeded) {
			return nil, srvTimeoutCtx.Err()
		}
	}

	if dOpts.webrtcOpts.SignalingServerAddress != "" {
		webrtcAddress := HostURI(dOpts.webrtcOpts.SignalingServerAddress, address)

		if dOpts.debug {
			logger.Debugw("trying WebRTC", "address", webrtcAddress)
		}

		conn, cached, err := dialFunc(ctx, "webrtc", webrtcAddress, buildKeyExtra(&dOpts), func() (ClientConn, error) {
			return dialWebRTC(ctx, webrtcAddress, &dOpts, logger)
		})
		if err == nil {
			if !cached {
				logger.Debug("connected via WebRTC")
			}
			return conn, nil
		}
		if err != nil && !errors.Is(err, ErrNoWebRTCSignaler) {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	if dOpts.debug {
		logger.Debugw("trying direct", "address", address)
	}
	conn, cached, err := dialDirectGRPC(ctx, address, &dOpts, logger)
	if err != nil {
		return nil, err
	}
	if !cached {
		logger.Debugw("connected directly", "address", address)
	}
	return conn, nil
}

func lookupSRV(ctx context.Context, host string) (string, uint16, error) {
	var records []*net.SRV
	var err error
	if ctxResolver := contextResolver(ctx); ctxResolver != nil {
		_, records, err = ctxResolver.LookupSRV(ctx, "webrtc", "tcp", host)
	} else {
		_, records, err = net.DefaultResolver.LookupSRV(ctx, "webrtc", "tcp", host)
	}
	if err != nil {
		return "", 0, err
	}
	if len(records) == 0 {
		return "", 0, errors.New("expected at least one SRV record")
	}
	return records[0].Target, records[0].Port, nil
}
