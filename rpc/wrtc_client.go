package rpc

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// ErrNoWebRTCSignaler happens if a gRPC request is made on a server that does not support
// signaling for WebRTC or explicitly not the host requested.
var ErrNoWebRTCSignaler = errors.New("no signaler present")

// DialWebRTCOptions control how WebRTC is utilized in a dial attempt.
type DialWebRTCOptions struct {
	// Disable prevents a WebRTC connection attempt.
	Disable bool

	// SignalingInsecure determines if the signaling connection is insecure.
	SignalingInsecure bool

	// SignalingServerAddress specifies the signaling server to
	// contact on behalf of this client for WebRTC communications.
	SignalingServerAddress string

	// SignalingAuthEntity is the entity to authenticate as to the signaler.
	SignalingAuthEntity string

	// SignalingExternalAuthAddress is the address to perform external auth yet.
	// This is unlikely to be needed since the signaler is typically in the same
	// place where authentication happens.
	SignalingExternalAuthAddress string

	// SignalingExternalAuthToEntity is the entity to authenticate for after
	// externally authenticating.
	// This is unlikely to be needed since the signaler is typically in the same
	// place where authentication happens.
	SignalingExternalAuthToEntity string

	// SignalingExternalAuthInsecure is whether or not the external auth server
	// is insecure.
	// This is unlikely to be needed since the signaler is typically in the same
	// place where authentication happens.
	SignalingExternalAuthInsecure bool

	// SignalingCreds are used to authenticate the request to the signaling server.
	SignalingCreds Credentials

	// SignalingExternalAuthAuthMaterial is used when the credentials for the signaler
	// have already been used to exchange an auth payload. In those cases this can be set
	// to bypass the Authenticate/AuthenticateTo rpc auth flow.
	SignalingExternalAuthAuthMaterial string

	// DisableTrickleICE controls whether to disable Trickle ICE or not.
	// Disabling Trickle ICE can slow down connection establishment.
	DisableTrickleICE bool

	// Config is the WebRTC specific configuration (i.e. ICE settings)
	Config *webrtc.Configuration

	// AllowAutoDetectAuthOptions allows authentication options to be automatically
	// detected. Only use this if you trust the signaling server.
	AllowAutoDetectAuthOptions bool
}

// DialWebRTC connects to the signaling service at the given address and attempts to establish
// a WebRTC connection with the corresponding peer reflected in the address.
// It provider client/server functionality for gRPC serviced over
// WebRTC data channels. The work is adapted from https://github.com/jsmouret/grpc-over-webrtc.
func DialWebRTC(
	ctx context.Context,
	signalingServer string,
	host string,
	logger golog.Logger,
	opts ...DialOption,
) (conn ClientConn, err error) {
	var dOpts dialOptions
	for _, opt := range opts {
		opt.apply(&dOpts)
	}
	dOpts.webrtcOpts.Disable = false
	dOpts.webrtcOpts.SignalingServerAddress = signalingServer
	return dialInner(ctx, host, logger, &dOpts)
}

func dialWebRTC(
	ctx context.Context,
	signalingServer string,
	host string,
	dOpts *dialOptions,
	logger golog.Logger,
) (ch *webrtcClientChannel, err error) {
	logger = logger.Named("webrtc")
	dialCtx, timeoutCancel := context.WithTimeout(ctx, getDefaultOfferDeadline())
	defer timeoutCancel()

	logger.Debugw(
		"connecting to signaling server",
		"signaling_server", signalingServer,
		"host", host,
	)

	dOptsCopy := *dOpts
	if dOpts.webrtcOpts.SignalingInsecure {
		dOptsCopy.insecure = true
	} else {
		dOptsCopy.insecure = false
	}

	// replace auth entity and creds
	dOptsCopy.authEntity = dOpts.webrtcOpts.SignalingAuthEntity
	dOptsCopy.creds = dOpts.webrtcOpts.SignalingCreds
	dOptsCopy.externalAuthAddr = dOpts.webrtcOpts.SignalingExternalAuthAddress
	dOptsCopy.externalAuthToEntity = dOpts.webrtcOpts.SignalingExternalAuthToEntity
	dOptsCopy.externalAuthInsecure = dOpts.webrtcOpts.SignalingExternalAuthInsecure
	dOptsCopy.externalAuthMaterial = dOpts.webrtcOpts.SignalingExternalAuthAuthMaterial

	// ignore AuthEntity when auth material is available.
	if dOptsCopy.authEntity == "" {
		if dOptsCopy.externalAuthAddr == "" {
			// if we are not doing external auth, then the entity is assumed to be the actual host.
			if dOpts.debug {
				logger.Debugw("auth entity empty; setting to host", "host", host)
			}
			dOptsCopy.authEntity = host
		} else {
			// otherwise it's the external auth address.
			if dOpts.debug {
				logger.Debugw("auth entity empty; setting to external auth address", "address", dOptsCopy.externalAuthAddr)
			}
			dOptsCopy.authEntity = dOptsCopy.externalAuthAddr
		}
	}

	conn, _, err := dialDirectGRPC(dialCtx, signalingServer, &dOptsCopy, logger)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = multierr.Combine(err, conn.Close())
	}()

	logger.Debugw("connected", "host", host)

	md := metadata.New(map[string]string{RPCHostMetadataField: host})
	signalCtx := metadata.NewOutgoingContext(dialCtx, md)

	signalingClient := webrtcpb.NewSignalingServiceClient(conn)
	configResp, err := signalingClient.OptionalWebRTCConfig(signalCtx, &webrtcpb.OptionalWebRTCConfigRequest{})
	if err != nil {
		// this would be where we would hit an unimplemented signaler error first.
		if s, ok := status.FromError(err); ok && (s.Code() == codes.Unimplemented ||
			(s.Code() == codes.InvalidArgument && s.Message() == hostNotAllowedMsg)) {
			return nil, ErrNoWebRTCSignaler
		}
		return nil, err
	}

	config := DefaultWebRTCConfiguration
	if dOptsCopy.webrtcOpts.Config != nil {
		config = *dOptsCopy.webrtcOpts.Config
	}
	extendedConfig := extendWebRTCConfig(&config, configResp.Config)
	pc, dc, err := newPeerConnectionForClient(ctx, extendedConfig, dOptsCopy.webrtcOpts.DisableTrickleICE, logger)
	if err != nil {
		return nil, err
	}
	var successful bool
	defer func() {
		if !successful {
			err = multierr.Combine(err, pc.Close())
		}
	}()

	exchangeCtx, exchangeCancel := context.WithCancel(signalCtx)
	defer exchangeCancel()

	errCh := make(chan error)
	sendErr := func(err error) {
		if s, ok := status.FromError(err); ok && strings.Contains(s.Message(), noActiveOfferStr) {
			return
		}
		select {
		case <-exchangeCtx.Done():
		case errCh <- err:
		}
	}
	var uuid string
	// only send once since exchange may end or ICE may end
	var sendDoneErrorOnce sync.Once
	sendDone := func() error {
		var err error
		sendDoneErrorOnce.Do(func() {
			_, err = signalingClient.CallUpdate(exchangeCtx, &webrtcpb.CallUpdateRequest{
				Uuid: uuid,
				Update: &webrtcpb.CallUpdateRequest_Done{
					Done: true,
				},
			})
		})
		return err
	}

	remoteDescSet := make(chan struct{})
	if !dOptsCopy.webrtcOpts.DisableTrickleICE {
		offer, err := pc.CreateOffer(nil)
		if err != nil {
			return nil, err
		}

		var callFlowWG sync.WaitGroup
		pc.OnICECandidate(func(i *webrtc.ICECandidate) {
			if exchangeCtx.Err() != nil {
				return
			}
			if i != nil {
				callFlowWG.Add(1)
			}
			// must spin off to unblock the ICE gatherer
			utils.PanicCapturingGo(func() {
				select {
				case <-remoteDescSet:
				case <-exchangeCtx.Done():
					return
				}
				if i == nil {
					callFlowWG.Wait()
					if err := sendDone(); err != nil {
						sendErr(err)
					}
					return
				}
				defer callFlowWG.Done()
				iProto := iceCandidateToProto(i)
				if _, err := signalingClient.CallUpdate(exchangeCtx, &webrtcpb.CallUpdateRequest{
					Uuid: uuid,
					Update: &webrtcpb.CallUpdateRequest_Candidate{
						Candidate: iProto,
					},
				}); err != nil {
					sendErr(err)
				}
			})
		})

		err = pc.SetLocalDescription(offer)
		if err != nil {
			return nil, err
		}
	}

	encodedSDP, err := encodeSDP(pc.LocalDescription())
	if err != nil {
		return nil, err
	}

	callClient, err := signalingClient.Call(signalCtx, &webrtcpb.CallRequest{Sdp: encodedSDP})
	if err != nil {
		return nil, err
	}

	// TODO(GOUT-11): do separate auth here
	if dOpts.externalAuthAddr != "" {
		// TODO(GOUT-11): prepare AuthenticateTo here
		// for client channel.
	} else if dOpts.creds.Type != "" { //nolint:staticcheck
		// TODO(GOUT-11): prepare Authenticate here
		// for client channel
	}

	clientCh := newWebRTCClientChannel(pc, dc, logger, dOpts.unaryInterceptor, dOpts.streamInterceptor)

	exchangeCandidates := func() error {
		haveInit := false
		for {
			if err := exchangeCtx.Err(); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}

			callResp, err := callClient.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					return err
				}
				return nil
			}
			switch s := callResp.Stage.(type) {
			case *webrtcpb.CallResponse_Init:
				if haveInit {
					return errors.New("got init stage more than once")
				}
				haveInit = true
				uuid = callResp.Uuid
				answer := webrtc.SessionDescription{}
				if err := decodeSDP(s.Init.Sdp, &answer); err != nil {
					return err
				}

				err = pc.SetRemoteDescription(answer)
				if err != nil {
					return err
				}
				close(remoteDescSet)

				if dOptsCopy.webrtcOpts.DisableTrickleICE {
					return sendDone()
				}
			case *webrtcpb.CallResponse_Update:
				if !haveInit {
					return errors.New("got update stage before init stage")
				}
				if callResp.Uuid != uuid {
					return errors.Errorf("uuid mismatch; have=%q want=%q", callResp.Uuid, uuid)
				}
				cand := iceCandidateFromProto(s.Update.Candidate)
				if err := pc.AddICECandidate(cand); err != nil {
					return err
				}
			default:
				return errors.Errorf("unexpected stage %T", s)
			}
		}
	}

	utils.PanicCapturingGoWithCallback(func() {
		if err := exchangeCandidates(); err != nil {
			sendErr(err)
		}
	}, func(err interface{}) {
		sendErr(fmt.Errorf("%v", err))
	})

	doCall := func() error {
		select {
		case <-exchangeCtx.Done():
			return multierr.Combine(exchangeCtx.Err(), clientCh.Close())
		case <-clientCh.Ready():
			return nil
		case err := <-errCh:
			return multierr.Combine(err, clientCh.Close())
		}
	}

	if callErr := doCall(); callErr != nil {
		var err error
		sendDoneErrorOnce.Do(func() {
			_, err = signalingClient.CallUpdate(exchangeCtx, &webrtcpb.CallUpdateRequest{
				Uuid: uuid,
				Update: &webrtcpb.CallUpdateRequest_Error{
					Error: ErrorToStatus(callErr).Proto(),
				},
			})
		})
		return nil, multierr.Combine(callErr, err)
	}
	if err := sendDone(); err != nil {
		return nil, err
	}
	successful = true
	return clientCh, nil
}
