package rpc

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// A WebRTCSignalingServer implements a signaling service for WebRTC by exchanging
// SDPs (https://webrtcforthecurious.com/docs/02-signaling/#what-is-the-session-description-protocol-sdp)
// via gRPC. The service consists of a many-to-many interaction where there are many callers
// and many answerers. The callers provide an SDP to the service which asks a corresponding
// waiting answerer to provide an SDP in exchange in order to establish a P2P connection between
// the two parties.
type WebRTCSignalingServer struct {
	webrtcpb.UnimplementedSignalingServiceServer
	mu                   sync.RWMutex
	callQueue            WebRTCCallQueue
	hostICEServers       map[string]hostICEServers
	webrtcConfigProvider WebRTCConfigProvider
}

// NewWebRTCSignalingServer makes a new signaling server that uses the given
// call queue and looks routes based on a given robot host.
func NewWebRTCSignalingServer(callQueue WebRTCCallQueue, webrtcConfigProvider WebRTCConfigProvider) *WebRTCSignalingServer {
	return &WebRTCSignalingServer{
		callQueue:            callQueue,
		hostICEServers:       map[string]hostICEServers{},
		webrtcConfigProvider: webrtcConfigProvider,
	}
}

// RPCHostMetadataField is the identifier of a host.
const RPCHostMetadataField = "rpc-host"

func hostFromCtx(ctx context.Context) (string, error) {
	hosts, err := hostsFromCtx(ctx)
	if err != nil {
		return "", err
	}
	if len(hosts) != 1 {
		return "", fmt.Errorf("expected 1 %s", RPCHostMetadataField)
	}
	return hosts[0], nil
}

const maxHostsInMetadata = 2

func hostsFromCtx(ctx context.Context) ([]string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md[RPCHostMetadataField]) == 0 {
		return nil, fmt.Errorf("expected %s to be set in metadata", RPCHostMetadataField)
	}
	if len(md[RPCHostMetadataField]) > maxHostsInMetadata {
		return nil, fmt.Errorf("too many %s", RPCHostMetadataField)
	}

	hostsCopy := make([]string, len(md[RPCHostMetadataField]))
	copy(hostsCopy, md[RPCHostMetadataField])
	for _, host := range hostsCopy {
		if host == "" {
			return nil, fmt.Errorf("expected non-empty %s", RPCHostMetadataField)
		}
	}
	return hostsCopy, nil
}

// Call is a request/offer to start a caller with the connected answerer.
func (srv *WebRTCSignalingServer) Call(req *webrtcpb.CallRequest, server webrtcpb.SignalingService_CallServer) error {
	ctx := server.Context()
	ctx, cancel := context.WithTimeout(ctx, getDefaultOfferDeadline())
	defer cancel()
	host, err := hostFromCtx(ctx)
	if err != nil {
		return err
	}
	uuid, respCh, respDone, cancel, err := srv.callQueue.SendOfferInit(ctx, host, req.Sdp, req.DisableTrickle)
	if err != nil {
		return err
	}
	defer cancel()

	var haveInit bool
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var resp WebRTCCallAnswer
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-respDone:
			return nil
		case resp = <-respCh:
		}
		if resp.Err != nil {
			return resp.Err
		}

		if !haveInit && resp.InitialSDP == nil {
			return errors.New("expected to have initial SDP if no error")
		}
		if !haveInit {
			haveInit = true
			if err := server.Send(&webrtcpb.CallResponse{
				Uuid: uuid,
				Stage: &webrtcpb.CallResponse_Init{
					Init: &webrtcpb.CallResponseInitStage{
						Sdp: *resp.InitialSDP,
					},
				},
			}); err != nil {
				return err
			}
		}

		if resp.Candidate == nil {
			continue
		}

		ip := iceCandidateInitToProto(*resp.Candidate)
		if err := server.Send(&webrtcpb.CallResponse{
			Uuid: uuid,
			Stage: &webrtcpb.CallResponse_Update{
				Update: &webrtcpb.CallResponseUpdateStage{
					Candidate: ip,
				},
			},
		}); err != nil {
			return err
		}
	}
}

// CallUpdate is used to send additional info in relation to a Call.
// In a world where https://github.com/grpc/grpc-web/issues/24 is fixed,
// this should be removed in favor of a bidirectional stream on Call.
func (srv *WebRTCSignalingServer) CallUpdate(ctx context.Context, req *webrtcpb.CallUpdateRequest) (*webrtcpb.CallUpdateResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, getDefaultOfferDeadline())
	defer cancel()
	host, err := hostFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	switch u := req.Update.(type) {
	case *webrtcpb.CallUpdateRequest_Candidate:
		cand := iceCandidateFromProto(u.Candidate)
		if err := srv.callQueue.SendOfferUpdate(ctx, host, req.Uuid, cand); err != nil {
			return nil, err
		}
	case *webrtcpb.CallUpdateRequest_Done:
		if err := srv.callQueue.SendOfferDone(ctx, host, req.Uuid); err != nil {
			return nil, err
		}
	default:
		return nil, errors.Errorf("unknown update stage %T", u)
	}
	return &webrtcpb.CallUpdateResponse{}, nil
}

type hostICEServers struct {
	Servers []*webrtcpb.ICEServer
	Expires time.Time
}

func (srv *WebRTCSignalingServer) additionalICEServers(ctx context.Context, hosts []string, cache bool) ([]*webrtcpb.ICEServer, error) {
	if srv.webrtcConfigProvider == nil {
		return nil, nil
	}
	hostsKey := strings.Join(hosts, ",")
	srv.mu.RLock()
	hostServers := srv.hostICEServers[hostsKey]
	srv.mu.RUnlock()
	if time.Now().Before(hostServers.Expires) {
		return hostServers.Servers, nil
	}
	config, err := srv.webrtcConfigProvider.Config(ctx)
	if err != nil {
		return nil, err
	}
	if cache {
		srv.mu.Lock()
		srv.hostICEServers[hostsKey] = hostICEServers{
			Servers: config.ICEServers,
			Expires: config.Expires,
		}
		srv.mu.Unlock()
	}
	return config.ICEServers, nil
}

// Note: We expect but do not enforce one host for one answer. If this is not true, a race
// can happen where we may double fetch additional ICE servers.
func (srv *WebRTCSignalingServer) clearAdditionalICEServers(hosts []string) {
	srv.mu.Lock()
	for _, host := range hosts {
		delete(srv.hostICEServers, host)
	}
	srv.mu.Unlock()
}

// Answer listens on call/offer queue forever responding with SDPs to agreed to calls.
// TODO(https://github.com/viamrobotics/core/issues/104): This should be authorized for robots only.
// Note: See SinalingAnswer.answer for the complementary side of this process.
func (srv *WebRTCSignalingServer) Answer(server webrtcpb.SignalingService_AnswerServer) error {
	ctx := server.Context()
	hosts, err := hostsFromCtx(ctx)
	if err != nil {
		return err
	}
	defer srv.clearAdditionalICEServers(hosts)

	offer, err := srv.callQueue.RecvOffer(ctx, hosts)
	if err != nil {
		return err
	}

	iceServers, err := srv.additionalICEServers(ctx, hosts, true)
	if err != nil {
		return err
	}

	// initialize
	uuid := offer.UUID()
	if err := server.Send(&webrtcpb.AnswerRequest{
		Uuid: uuid,
		Stage: &webrtcpb.AnswerRequest_Init{
			Init: &webrtcpb.AnswerRequestInitStage{
				Sdp: offer.SDP(),
				OptionalConfig: &webrtcpb.WebRTCConfig{
					AdditionalIceServers: iceServers,
					DisableTrickle:       offer.DisableTrickleICE(),
				},
			},
		},
	}); err != nil {
		return err
	}

	offerCtx, offerCtxCancel := context.WithTimeout(ctx, getDefaultOfferDeadline())
	var answererStoppedExchange bool
	callerLoop := func() error {
		defer func() {
			if !answererStoppedExchange {
				utils.UncheckedError(server.Send(&webrtcpb.AnswerRequest{
					Uuid: uuid,
					Stage: &webrtcpb.AnswerRequest_Done{
						Done: &webrtcpb.AnswerRequestDoneStage{},
					},
				}))
			}
		}()
		for {
			select {
			case <-offerCtx.Done():
				return offerCtx.Err()
			case <-offer.CallerDone():
				callerErr := offer.CallerErr()
				if callerErr != nil {
					if err := server.Send(&webrtcpb.AnswerRequest{
						Uuid: uuid,
						Stage: &webrtcpb.AnswerRequest_Error{
							Error: &webrtcpb.AnswerRequestErrorStage{
								Status: ErrorToStatus(callerErr).Proto(),
							},
						},
					}); err != nil {
						return multierr.Combine(callerErr, err)
					}
				}
				return callerErr
			case cand := <-offer.CallerCandidates():
				ip := iceCandidateInitToProto(cand)
				if err := server.Send(&webrtcpb.AnswerRequest{
					Uuid: uuid,
					Stage: &webrtcpb.AnswerRequest_Update{
						Update: &webrtcpb.AnswerRequestUpdateStage{
							Candidate: ip,
						},
					},
				}); err != nil {
					return err
				}
			}
		}
	}

	answererLoop := func() error {
		haveInit := false
		for {
			answer, err := server.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					return err
				}
				return nil
			}

			switch s := answer.Stage.(type) {
			case *webrtcpb.AnswerResponse_Init:
				if haveInit {
					return errors.New("got init stage more than once")
				}
				haveInit = true
				init := s.Init

				ans := WebRTCCallAnswer{InitialSDP: &init.Sdp}
				if err := offer.AnswererRespond(server.Context(), ans); err != nil {
					return err
				}
			case *webrtcpb.AnswerResponse_Update:
				if !haveInit {
					return errors.New("got update stage before init stage")
				}
				if answer.Uuid != uuid {
					return errors.Errorf("uuid mismatch; have=%q want=%q", answer.Uuid, uuid)
				}
				cand := iceCandidateFromProto(s.Update.Candidate)
				if err := offer.AnswererRespond(server.Context(), WebRTCCallAnswer{
					Candidate: &cand,
				}); err != nil {
					return err
				}
			case *webrtcpb.AnswerResponse_Done:
				if !haveInit {
					return errors.New("got done stage before init stage")
				}
				if answer.Uuid != uuid {
					return errors.Errorf("uuid mismatch; have=%q want=%q", answer.Uuid, uuid)
				}
				return nil
			case *webrtcpb.AnswerResponse_Error:
				respStatus := status.FromProto(s.Error.Status)
				ans := WebRTCCallAnswer{Err: respStatus.Err()}
				answererStoppedExchange = true
				offerCtxCancel() // and stop exchange
				return offer.AnswererRespond(server.Context(), ans)
			default:
				return errors.Errorf("unexpected stage %T", s)
			}
		}
	}

	callerErrCh := make(chan error, 1)
	utils.PanicCapturingGo(func() {
		defer close(callerErrCh)
		if err := callerLoop(); err != nil {
			callerErrCh <- err
		}
	})

	// ensure we wait on the error channel
	return func() (err error) {
		defer func() {
			err = multierr.Combine(err, <-callerErrCh)
		}()
		defer func() {
			err = multierr.Combine(err, offer.AnswererDone(server.Context()))
		}()
		defer func() {
			if err != nil {
				// one side failed, cancel the other
				offerCtxCancel()
			}
		}()
		return answererLoop()
	}()
}

// OptionalWebRTCConfig returns any WebRTC configuration the caller may want to use.
func (srv *WebRTCSignalingServer) OptionalWebRTCConfig(
	ctx context.Context,
	req *webrtcpb.OptionalWebRTCConfigRequest,
) (*webrtcpb.OptionalWebRTCConfigResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, getDefaultOfferDeadline())
	defer cancel()
	hosts, err := hostsFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	iceServers, err := srv.additionalICEServers(ctx, hosts, false)
	if err != nil {
		return nil, err
	}
	return &webrtcpb.OptionalWebRTCConfigResponse{Config: &webrtcpb.WebRTCConfig{
		AdditionalIceServers: iceServers,
	}}, nil
}
