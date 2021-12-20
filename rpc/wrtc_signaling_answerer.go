package rpc

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// A webrtcSignalingAnswerer listens for and answers calls with a given signaling service. It is
// directly connected to a Server that will handle the actual calls/connections over WebRTC
// data channels.
type webrtcSignalingAnswerer struct {
	address                 string
	host                    string
	client                  webrtcpb.SignalingService_AnswerClient
	server                  *webrtcServer
	dialOpts                []DialOption
	webrtcConfig            webrtc.Configuration
	activeBackgroundWorkers sync.WaitGroup
	cancelBackgroundWorkers func()
	closeCtx                context.Context
	logger                  golog.Logger
}

// newWebRTCSignalingAnswerer makes an answerer that will connect to and listen for calls at the given
// address. Note that using this assumes that the connection at the given address is secure and
// assumed that all calls are authenticated. Random ports will be opened on this host to establish
// connections as a means to service ICE (https://webrtcforthecurious.com/docs/03-connecting/#how-does-it-work).
func newWebRTCSignalingAnswerer(address, host string, server *webrtcServer, dialOpts []DialOption, webrtcConfig webrtc.Configuration, logger golog.Logger) *webrtcSignalingAnswerer {
	closeCtx, cancel := context.WithCancel(context.Background())
	return &webrtcSignalingAnswerer{
		address:                 address,
		host:                    host,
		server:                  server,
		dialOpts:                dialOpts,
		webrtcConfig:            webrtcConfig,
		cancelBackgroundWorkers: cancel,
		closeCtx:                closeCtx,
		logger:                  logger,
	}
}

const answererReconnectWait = time.Second

// Start connects to the signaling service and listens forever until instructed to stop
// via Stop.
func (ans *webrtcSignalingAnswerer) Start() error {
	var connInUse ClientConn
	var connMu sync.Mutex
	reconnect := func() error {
		connMu.Lock()
		conn := connInUse
		connMu.Unlock()
		client := webrtcpb.NewSignalingServiceClient(conn)
		md := metadata.New(map[string]string{RPCHostMetadataField: ans.host})
		answerCtx := metadata.NewOutgoingContext(ans.closeCtx, md)
		answerClient, err := client.Answer(answerCtx)
		if err != nil {
			return multierr.Combine(err, conn.Close())
		}
		ans.client = answerClient
		return nil
	}
	fullReconnect := func() error {
		connMu.Lock()
		conn := connInUse
		connMu.Unlock()
		if conn != nil {
			if err := conn.Close(); err != nil {
				ans.logger.Errorw("error closing existing signaling connection", "error", err)
			}
		}
		setupCtx, timeoutCancel := context.WithTimeout(ans.closeCtx, 5*time.Second)
		defer timeoutCancel()
		conn, err := Dial(setupCtx, ans.address, ans.logger, ans.dialOpts...)
		if err != nil {
			return err
		}
		connMu.Lock()
		connInUse = conn
		connMu.Unlock()
		return reconnect()
	}

	ans.activeBackgroundWorkers.Add(1)
	utils.ManagedGo(func() {
		for {
			select {
			case <-ans.closeCtx.Done():
				return
			default:
			}
			if err := ans.answer(); err != nil {
				if errors.Is(err, io.EOF) {
					// normal error
					if connectErr := reconnect(); connectErr != nil {
						ans.logger.Errorw("error reconnecting answer client", "error", err, "reconnect_err", connectErr)
						continue
					}
				} else if utils.FilterOutError(err, context.Canceled) != nil {
					// exceptional error
					shouldLogError := false
					if !errors.Is(err, errWebRTCSignalingAnswererDisconnected) {
						shouldLogError = true
					}
					if shouldLogError {
						ans.logger.Errorw("error answering", "error", err)
					}
					for {
						if shouldLogError {
							ans.logger.Debugw("reconnecting answer client", "in", answererReconnectWait.String())
						}
						if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
							return
						}
						if connectErr := fullReconnect(); connectErr != nil {
							ans.logger.Errorw("error reconnecting answer client", "error", err, "reconnect_err", connectErr)
							continue
						}
						if shouldLogError {
							ans.logger.Debug("reconnected answer client")
						}
						break
					}
				}
			}
		}
	}, func() {
		defer ans.activeBackgroundWorkers.Done()
		defer func() {
			connMu.Lock()
			conn := connInUse
			connMu.Unlock()
			if conn == nil {
				return
			}
			if err := conn.Close(); err != nil {
				ans.logger.Errorw("error closing signaling connection", "error", err)
			}
		}()
		defer func() {
			if ans.client == nil {
				return
			}
			if err := ans.client.CloseSend(); err != nil {
				ans.logger.Errorw("error closing send side of answering client", "error", err)
			}
		}()
	})

	return nil
}

// Stop waits for the answer to stop listening and return.
func (ans *webrtcSignalingAnswerer) Stop() {
	ans.cancelBackgroundWorkers()
	ans.activeBackgroundWorkers.Wait()
}

var errWebRTCSignalingAnswererDisconnected = errors.New("signaling answerer disconnected")

// answer accepts a single call offer, responds with a corresponding SDP, and
// attempts to establish a WebRTC connection with the caller via ICE. Once established,
// the designated WebRTC data channel is passed off to the underlying Server which
// is then used as the server end of a gRPC connection.
// Note: right now the implementation of WebRTCSignalingAnswerer and SignalingServer are bound
// to each other in that only one offer is completely answered at a time. In order to
// change this, this should really be answerAll and hold the state of all active
// offers (by UUID).
func (ans *webrtcSignalingAnswerer) answer() (err error) {
	if ans.client == nil {
		return errWebRTCSignalingAnswererDisconnected
	}
	resp, err := ans.client.Recv()
	if err != nil {
		return err
	}

	uuid := resp.Uuid
	initStage, ok := resp.Stage.(*webrtcpb.AnswerRequest_Init)
	if !ok {
		err := errors.Errorf("expected first stage to be init; got %T", resp.Stage)
		return ans.client.Send(&webrtcpb.AnswerResponse{
			Uuid: uuid,
			Stage: &webrtcpb.AnswerResponse_Error{
				Error: &webrtcpb.AnswerResponseErrorStage{
					Status: ErrorToStatus(err).Proto(),
				},
			},
		})
	}
	init := initStage.Init

	disableTrickle := false
	if init.OptionalConfig != nil {
		disableTrickle = init.OptionalConfig.DisableTrickle
	}
	extendedConfig := extendWebRTCConfig(&ans.webrtcConfig, init.OptionalConfig)
	pc, dc, err := newPeerConnectionForServer(
		ans.closeCtx,
		init.Sdp,
		extendedConfig,
		disableTrickle,
		ans.logger,
	)
	if err != nil {
		return ans.client.Send(&webrtcpb.AnswerResponse{
			Uuid: uuid,
			Stage: &webrtcpb.AnswerResponse_Error{
				Error: &webrtcpb.AnswerResponseErrorStage{
					Status: ErrorToStatus(err).Proto(),
				},
			},
		})
	}
	var successful bool
	defer func() {
		if !(successful && err == nil) {
			err = multierr.Combine(err, pc.Close())
		}
	}()

	// only send once since exchange may end or ICE may end
	var sendDoneErrorOnce sync.Once
	sendDone := func() error {
		var err error
		sendDoneErrorOnce.Do(func() {
			err = ans.client.Send(&webrtcpb.AnswerResponse{
				Uuid: uuid,
				Stage: &webrtcpb.AnswerResponse_Done{
					Done: &webrtcpb.AnswerResponseDoneStage{},
				},
			})
		})
		return err
	}

	errCh := make(chan interface{})
	exchangeCtx, exchangeCancel := context.WithTimeout(ans.closeCtx, webrtcConnectionTimeout)
	defer exchangeCancel()
	sendErr := func(err interface{}) {
		select {
		case <-exchangeCtx.Done():
		case errCh <- err:
		}
	}

	initSent := make(chan struct{})
	if !init.OptionalConfig.DisableTrickle {
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			return err
		}

		pc.OnICECandidate(func(i *webrtc.ICECandidate) {
			if exchangeCtx.Err() != nil {
				return
			}
			select {
			case <-initSent:
			case <-exchangeCtx.Done():
				return
			}
			if i == nil {
				if err := sendDone(); err != nil {
					sendErr(err)
				}
				return
			}
			iProto := iceCandidateToProto(i)
			if err := ans.client.Send(&webrtcpb.AnswerResponse{
				Uuid: uuid,
				Stage: &webrtcpb.AnswerResponse_Update{
					Update: &webrtcpb.AnswerResponseUpdateStage{
						Candidate: iProto,
					},
				},
			}); err != nil {
				sendErr(err)
			}
		})

		err = pc.SetLocalDescription(answer)
		if err != nil {
			return err
		}
	}

	encodedSDP, err := encodeSDP(pc.LocalDescription())
	if err != nil {
		return ans.client.Send(&webrtcpb.AnswerResponse{
			Uuid: uuid,
			Stage: &webrtcpb.AnswerResponse_Error{
				Error: &webrtcpb.AnswerResponseErrorStage{
					Status: ErrorToStatus(err).Proto(),
				},
			},
		})
	}

	if err := ans.client.Send(&webrtcpb.AnswerResponse{
		Uuid: uuid,
		Stage: &webrtcpb.AnswerResponse_Init{
			Init: &webrtcpb.AnswerResponseInitStage{
				Sdp: encodedSDP,
			},
		},
	}); err != nil {
		return err
	}
	close(initSent)

	serverChannel := ans.server.NewChannel(pc, dc)

	if !init.OptionalConfig.DisableTrickle {
		exchangeCandidates := func() error {
			for {
				if err := exchangeCtx.Err(); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}

				ansResp, err := ans.client.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						return err
					}
					return nil
				}
				switch s := ansResp.Stage.(type) {
				case *webrtcpb.AnswerRequest_Init:
				case *webrtcpb.AnswerRequest_Update:
					if ansResp.Uuid != uuid {
						return errors.Errorf("uuid mismatch; have=%q want=%q", ansResp.Uuid, uuid)
					}
					cand := iceCandidateFromProto(s.Update.Candidate)
					if err := pc.AddICECandidate(cand); err != nil {
						return err
					}
				case *webrtcpb.AnswerRequest_Done:
					return nil
				case *webrtcpb.AnswerRequest_Error:
					respStatus := status.FromProto(s.Error.Status)
					return fmt.Errorf("error from requester: %w", respStatus.Err())
				default:
					return errors.Errorf("unexpected stage %T", s)
				}
			}
		}

		done := make(chan struct{})
		defer func() { <-done }()
		utils.PanicCapturingGoWithCallback(func() {
			defer close(done)
			if err := exchangeCandidates(); err != nil {
				sendErr(err)
			}
		}, func(err interface{}) {
			sendErr(err)
		})
	}

	doAnswer := func() error {
		select {
		case <-exchangeCtx.Done():
			return multierr.Combine(exchangeCtx.Err(), serverChannel.Close())
		case <-serverChannel.Ready():
			return nil
		case <-errCh:
			return multierr.Combine(err, serverChannel.Close())
		}
	}

	if answerErr := doAnswer(); answerErr != nil {
		var err error
		sendDoneErrorOnce.Do(func() {
			err = ans.client.Send(&webrtcpb.AnswerResponse{
				Uuid: uuid,
				Stage: &webrtcpb.AnswerResponse_Error{
					Error: &webrtcpb.AnswerResponseErrorStage{
						Status: ErrorToStatus(answerErr).Proto(),
					},
				},
			})
		})
		return err
	}
	if err := sendDone(); err != nil {
		return err
	}
	successful = true
	return nil
}
