package rpc

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

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

const testDelayAnswererNegotiationVar = "TEST_DELAY_ANSWERER_NEGOTIATION"

// A webrtcSignalingAnswerer listens for and answers calls with a given signaling service. It is
// directly connected to a Server that will handle the actual calls/connections over WebRTC
// data channels.
type webrtcSignalingAnswerer struct {
	startStopMu sync.Mutex // startStopMu guards the Start and Stop methods so they do not happen concurrently.

	address                 string
	hosts                   []string
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
func newWebRTCSignalingAnswerer(
	address string,
	hosts []string,
	server *webrtcServer,
	dialOpts []DialOption,
	webrtcConfig webrtc.Configuration,
	logger golog.Logger,
) *webrtcSignalingAnswerer {
	dialOptsCopy := make([]DialOption, len(dialOpts))
	copy(dialOptsCopy, dialOpts)
	dialOptsCopy = append(dialOptsCopy, WithWebRTCOptions(DialWebRTCOptions{Disable: true}))
	closeCtx, cancel := context.WithCancel(context.Background())
	return &webrtcSignalingAnswerer{
		address:                 address,
		hosts:                   hosts,
		server:                  server,
		dialOpts:                dialOptsCopy,
		webrtcConfig:            webrtcConfig,
		cancelBackgroundWorkers: cancel,
		closeCtx:                closeCtx,
		logger:                  logger,
	}
}

const (
	defaultMaxAnswerers   = 2
	answererReconnectWait = time.Second
)

// Start connects to the signaling service and listens forever until instructed to stop
// via Stop.
func (ans *webrtcSignalingAnswerer) Start() {
	ans.startStopMu.Lock()
	defer ans.startStopMu.Unlock()

	for i := 0; i < defaultMaxAnswerers; i++ {
		ans.startAnswerer()
	}
}

func checkExceptionalError(err error) error {
	s, isGRPCErr := status.FromError(err)
	if err == nil || errors.Is(err, io.EOF) ||
		utils.FilterOutError(err, context.Canceled) == nil ||
		(isGRPCErr &&
			(s.Code() == codes.DeadlineExceeded ||
				s.Code() == codes.Canceled ||
				strings.Contains(s.Message(), "too_many_pings") ||
				// RSDK-3025: Cloud Run has a max one hour timeout which will terminate gRPC
				// streams, but leave the underlying connection open.
				strings.Contains(s.Message(), "upstream max stream duration reached"))) {
		return nil
	}
	return err
}

func (ans *webrtcSignalingAnswerer) startAnswerer() {
	var connInUse ClientConn
	var connMu sync.Mutex
	reconnect := func() error {
		connMu.Lock()
		conn := connInUse
		connMu.Unlock()
		if conn != nil {
			if err := checkExceptionalError(conn.Close()); err != nil {
				ans.logger.Errorw("error closing existing signaling connection", "error", err)
			}
		}
		setupCtx, timeoutCancel := context.WithTimeout(ans.closeCtx, 10*time.Second)
		defer timeoutCancel()
		conn, err := Dial(setupCtx, ans.address, ans.logger, ans.dialOpts...)
		if err != nil {
			return err
		}
		connMu.Lock()
		connInUse = conn
		connMu.Unlock()
		return nil
	}
	newAnswer := func() (webrtcpb.SignalingService_AnswerClient, error) {
		connMu.Lock()
		conn := connInUse
		connMu.Unlock()
		if conn == nil {
			if err := reconnect(); err != nil {
				return nil, err
			}
		}
		connMu.Lock()
		conn = connInUse
		connMu.Unlock()
		client := webrtcpb.NewSignalingServiceClient(conn)
		md := metadata.New(nil)
		md.Append(RPCHostMetadataField, ans.hosts...)
		answerCtx := metadata.NewOutgoingContext(ans.closeCtx, md)
		answerClient, err := client.Answer(answerCtx)
		if err != nil {
			return nil, multierr.Combine(err, conn.Close())
		}
		return answerClient, nil
	}
	ans.activeBackgroundWorkers.Add(1)
	utils.ManagedGo(func() {
		var client webrtcpb.SignalingService_AnswerClient
		defer func() {
			if client == nil {
				return
			}
			if err := client.CloseSend(); err != nil {
				ans.logger.Errorw("error closing send side of answering client", "error", err)
			}
		}()
		for {
			select {
			case <-ans.closeCtx.Done():
				return
			default:
			}
			var err error
			client, err = newAnswer()
			if err == nil {
				err = ans.answer(client)
			}
			// Exceptional errors represent a broken connection and require reconnecting. Common
			// errors represent that an operation has failed, but can be safely retried over the
			// existing connection.
			if checkExceptionalError(err) == nil {
				continue
			}

			ans.logger.Errorw("error answering", "error", err)
			for {
				ans.logger.Debugw("reconnecting answer client", "in", answererReconnectWait.String())
				if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
					return
				}
				if connectErr := reconnect(); connectErr != nil {
					ans.logger.Errorw("error reconnecting answer client", "error", err, "reconnect_err", connectErr)
					continue
				}
				ans.logger.Debug("reconnected answer client")
				break
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

			if err := checkExceptionalError(conn.Close()); err != nil {
				ans.logger.Errorw("error closing signaling connection", "error", err)
			}
		}()
	})
}

// Stop waits for the answer to stop listening and return.
func (ans *webrtcSignalingAnswerer) Stop() {
	ans.startStopMu.Lock()
	defer ans.startStopMu.Unlock()

	ans.cancelBackgroundWorkers()
	ans.activeBackgroundWorkers.Wait()
}

// answer accepts a single call offer, responds with a corresponding SDP, and
// attempts to establish a WebRTC connection with the caller via ICE. Once established,
// the designated WebRTC data channel is passed off to the underlying Server which
// is then used as the server end of a gRPC connection.
func (ans *webrtcSignalingAnswerer) answer(client webrtcpb.SignalingService_AnswerClient) (err error) {
	resp, err := client.Recv()
	if err != nil {
		return err
	}

	uuid := resp.Uuid
	initStage, ok := resp.Stage.(*webrtcpb.AnswerRequest_Init)
	if !ok {
		err := errors.Errorf("expected first stage to be init; got %T", resp.Stage)
		return client.Send(&webrtcpb.AnswerResponse{
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
	pc, dc, err := newPeerConnectionForServer(
		ans.closeCtx,
		init.Sdp,
		ans.webrtcConfig,
		disableTrickle,
		ans.logger,
	)
	if err != nil {
		return client.Send(&webrtcpb.AnswerResponse{
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
			err = client.Send(&webrtcpb.AnswerResponse{
				Uuid: uuid,
				Stage: &webrtcpb.AnswerResponse_Done{
					Done: &webrtcpb.AnswerResponseDoneStage{},
				},
			})
		})
		return err
	}

	var exchangeCtx context.Context
	var exchangeCancel func()
	if initStage.Init.Deadline != nil {
		exchangeCtx, exchangeCancel = context.WithDeadline(ans.closeCtx, initStage.Init.Deadline.AsTime())
	} else {
		exchangeCtx, exchangeCancel = context.WithTimeout(ans.closeCtx, getDefaultOfferDeadline())
	}

	errCh := make(chan interface{})
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

		var pendingCandidates sync.WaitGroup
		waitOneHost := make(chan struct{})
		var waitOneHostOnce sync.Once
		pc.OnICECandidate(func(icecandidate *webrtc.ICECandidate) {
			if exchangeCtx.Err() != nil {
				return
			}
			if icecandidate != nil {
				pendingCandidates.Add(1)
				if icecandidate.Typ == webrtc.ICECandidateTypeHost {
					waitOneHostOnce.Do(func() {
						close(waitOneHost)
					})
				}
			}
			// must spin off to unblock the ICE gatherer
			ans.activeBackgroundWorkers.Add(1)
			utils.PanicCapturingGo(func() {
				defer ans.activeBackgroundWorkers.Done()

				if icecandidate != nil {
					defer pendingCandidates.Done()
				}

				select {
				case <-initSent:
				case <-exchangeCtx.Done():
					return
				}
				// there are no more candidates coming during this negotiation
				if icecandidate == nil {
					if _, ok := os.LookupEnv(testDelayAnswererNegotiationVar); ok {
						// RSDK-4293: Introducing a sleep here replicates the conditions
						// for a prior goroutine leak.
						ans.logger.Debug("Sleeping to delay the end of the negotiation")
						time.Sleep(1 * time.Second)
					}
					pendingCandidates.Wait()
					if err := sendDone(); err != nil {
						sendErr(err)
					}
					return
				}
				iProto := iceCandidateToProto(icecandidate)
				if err := client.Send(&webrtcpb.AnswerResponse{
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
		})

		err = pc.SetLocalDescription(answer)
		if err != nil {
			return err
		}

		select {
		case <-exchangeCtx.Done():
			return exchangeCtx.Err()
		case <-waitOneHost:
		}
	}

	encodedSDP, err := encodeSDP(pc.LocalDescription())
	if err != nil {
		return client.Send(&webrtcpb.AnswerResponse{
			Uuid: uuid,
			Stage: &webrtcpb.AnswerResponse_Error{
				Error: &webrtcpb.AnswerResponseErrorStage{
					Status: ErrorToStatus(err).Proto(),
				},
			},
		})
	}

	if err := client.Send(&webrtcpb.AnswerResponse{
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

	serverChannel := ans.server.NewChannel(pc, dc, ans.hosts)

	if !init.OptionalConfig.DisableTrickle {
		exchangeCandidates := func() error {
			for {
				if err := exchangeCtx.Err(); err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}

				ansResp, err := client.Recv()
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
			err = client.Send(&webrtcpb.AnswerResponse{
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
