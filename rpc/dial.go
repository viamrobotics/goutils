package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/edaniels/golog"
)

// Dial attempts to make the most convenient connection to the given address. It first tries a direct
// connection if the address is an IP. It next tries to connect to the local version of the host followed
// by a WebRTC brokered connection.
// TODO(https://github.com/viamrobotics/core/issues/111): figure out decent way to handle reconnect on connection termination.
func Dial(ctx context.Context, address string, logger golog.Logger, opts ...DialOption) (ClientConn, error) {
	var dOpts dialOptions
	for _, opt := range opts {
		opt.apply(&dOpts)
	}
	if dOpts.authEntity == "" {
		dOpts.authEntity = address
	}

	var host string
	var port string
	if strings.ContainsRune(address, ':') {
		var err error
		host, port, err = net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
	} else {
		host = address
	}

	if addr := net.ParseIP(host); addr == nil && false {
		localHost := fmt.Sprintf("local.%s", host)
		if _, err := lookupHost(ctx, localHost); err == nil {
			var localAddress string
			if port == "" {
				localAddress = fmt.Sprintf("%s:80", localHost)
			} else {
				localAddress = fmt.Sprintf("%s:%s", localHost, port)
			}
			// TODO(https://github.com/viamrobotics/core/issues/103): This needs to authenticate the server so we don't have a confused
			// deputy.
			localCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if conn, cached, err := dialDirectGRPC(localCtx, localAddress, &dOpts, logger); err == nil {
				if !cached {
					logger.Debugw("connected directly via local host", "address", localAddress)
				}
				return conn, nil
			} else if ctx.Err() != nil { // do not care about local timeout
				return nil, ctx.Err()
			}
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	dOpts.webrtcOpts.Insecure = dOpts.insecure
	if target, port, err := lookupSRV(ctx, host); err == nil {
		dOpts.webrtcOpts.Insecure = port != 443
		dOpts.webrtcOpts.SignalingServer = fmt.Sprintf("%s:%d", target, port)
	} else if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, ctx.Err()
	}

	if dOpts.webrtcOpts.SignalingServer != "" {
		webrtcAddress := HostURI(dOpts.webrtcOpts.SignalingServer, address)

		conn, cached, err := dialFunc(ctx, "webrtc", webrtcAddress, func() (ClientConn, error) {
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

	conn, cached, err := dialDirectGRPC(ctx, address, &dOpts, logger)
	if err != nil {
		return nil, err
	}
	if !cached {
		logger.Debugw("connected directly", "address", address)
	}
	return conn, nil
}

func lookupHost(ctx context.Context, host string) (addrs []string, err error) {
	if ctxResolver := contextResolver(ctx); ctxResolver != nil {
		return ctxResolver.LookupHost(ctx, host)
	}
	return net.DefaultResolver.LookupHost(ctx, host)
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
