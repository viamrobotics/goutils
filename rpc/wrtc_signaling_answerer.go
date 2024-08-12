package rpc

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/viamrobotics/webrtc/v3"
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

	address      string
	hosts        []string
	server       *webrtcServer
	dialOpts     []DialOption
	webrtcConfig webrtc.Configuration

	// bgWorkersMu should be `Lock`ed in `Stop` to `Wait` on ongoing background workers in startAnswerer/answer.
	// bgWorkersMu should be `RLock`ed when starting a new background worker (allow concurrent background workers) to
	// `Add` to bgWorkers.
	bgWorkersMu     sync.RWMutex
	bgWorkers       sync.WaitGroup
	cancelBgWorkers func()

	// conn is used to share the direct gRPC connection used by the answerer workers. As direct gRPC connections
	// reconnect on their own, custom reconnect logic is not needed. However, keepalives are necessary for the connection
	// to realize it's been disconnected quickly and start reconnecting.
	connMu sync.Mutex
	conn   ClientConn

	closeCtx context.Context
	logger   utils.ZapCompatibleLogger
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
	logger utils.ZapCompatibleLogger,
) *webrtcSignalingAnswerer {
	dialOptsCopy := make([]DialOption, len(dialOpts))
	copy(dialOptsCopy, dialOpts)
	dialOptsCopy = append(dialOptsCopy, WithWebRTCOptions(DialWebRTCOptions{Disable: true}))
	closeCtx, cancel := context.WithCancel(context.Background())
	return &webrtcSignalingAnswerer{
		address:         address,
		hosts:           hosts,
		server:          server,
		dialOpts:        dialOptsCopy,
		webrtcConfig:    webrtcConfig,
		cancelBgWorkers: cancel,
		closeCtx:        closeCtx,
		logger:          logger,
	}
}

const (
	defaultMaxAnswerers    = 2
	answererConnectTimeout = 10 * time.Second
	answererReconnectWait  = time.Second
)

// Start connects to the signaling service and listens forever until instructed to stop
// via Stop. Start cannot be called more than once before a Stop().
func (ans *webrtcSignalingAnswerer) Start() {
	ans.startStopMu.Lock()
	defer ans.startStopMu.Unlock()

	ans.bgWorkersMu.RLock()
	ans.bgWorkers.Add(1)
	ans.bgWorkersMu.RUnlock()

	// attempt to make connection in a loop
	utils.ManagedGo(func() {
		for ans.conn == nil {
			select {
			case <-ans.closeCtx.Done():
				return
			default:
			}

			setupCtx, timeoutCancel := context.WithTimeout(ans.closeCtx, answererConnectTimeout)
			conn, err := Dial(setupCtx, ans.address, ans.logger, ans.dialOpts...)
			timeoutCancel()
			if err != nil {
				ans.logger.Errorw("error connecting answer client", "error", err)
				if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
					return
				}
				continue
			}
			ans.connMu.Lock()
			ans.conn = conn
			ans.connMu.Unlock()
		}
		// spin off the actual answerer workers
		for i := 0; i < defaultMaxAnswerers; i++ {
			ans.startAnswerer()
		}
	}, func() {
		ans.bgWorkers.Done()
	})
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
	newAnswer := func() (webrtcpb.SignalingService_AnswerClient, error) {
		ans.connMu.Lock()
		conn := ans.conn
		ans.connMu.Unlock()
		client := webrtcpb.NewSignalingServiceClient(conn)
		md := metadata.New(nil)
		md.Append(RPCHostMetadataField, ans.hosts...)
		answerCtx := metadata.NewOutgoingContext(ans.closeCtx, md)
		answerClient, err := client.Answer(answerCtx)
		if err != nil {
			return nil, err
		}
		return answerClient, nil
	}
	ans.bgWorkersMu.RLock()
	ans.bgWorkers.Add(1)
	ans.bgWorkersMu.RUnlock()

	// Check if closeCtx has errored: underlying answerer may have been
	// `Stop`ped, in which case we mark this answer worker as `Done` and
	// return.
	if err := ans.closeCtx.Err(); err != nil {
		ans.bgWorkers.Done()
		return
	}

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
			// `newAnswer` opens a bidi grpc stream to the signaling server. But otherwise sends no requests.
			client, err = newAnswer()
			receivedInitRequest := false
			if err == nil {
				// `ans.answer` will send the initial message to the signaling server that says it
				// is ready to accept connections. Then it waits, typically for a long time, for a
				// caller to show up. Which is when the signaling server will send a
				// response. `ans.answer` then follows with gathering ICE candidates and learning of
				// the caller's ICE candidates to create a working WebRTC PeerConnection. If
				// successful, the `PeerConnection` + `webrtcServerChannel` will be registered and
				// available for the `webrtcServer`.
				receivedInitRequest, err = ans.answer(client)
			}

			switch {
			case err == nil:
			case receivedInitRequest && err != nil:
				// We received an error while trying to connect to a caller/peer.
				ans.logger.Errorw("error connecting to peer", "error", err)
				if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
					return
				}
			case !receivedInitRequest && err != nil:
				// Exceptional errors represent a broken connection to the signaling server. While
				// direct gRPC connections will reconnect on their own, we should wait a little
				// before trying to call again. Common errors represent that an operation has
				// failed, but can be safely retried over the existing connection.
				if checkExceptionalError(err) != nil {
					ans.logger.Warnw("error communicating with signaling server", "error", err)
					if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
						return
					}
				}
			}
		}
	}, func() {
		ans.bgWorkers.Done()
	})
}

// Stop waits for the answer to stop listening and return.
func (ans *webrtcSignalingAnswerer) Stop() {
	ans.startStopMu.Lock()
	defer ans.startStopMu.Unlock()

	ans.cancelBgWorkers()
	ans.bgWorkersMu.Lock()
	ans.bgWorkers.Wait()
	ans.bgWorkersMu.Unlock()

	ans.connMu.Lock()
	defer ans.connMu.Unlock()
	if ans.conn != nil {
		if err := checkExceptionalError(ans.conn.Close()); err != nil {
			ans.logger.Errorw("error closing signaling connection", "error", err)
		}
		ans.conn = nil
	}
}

// answer accepts a single call offer, responds with a corresponding SDP, and
// attempts to establish a WebRTC connection with the caller via ICE. Once established,
// the designated WebRTC data channel is passed off to the underlying Server which
// is then used as the server end of a gRPC connection.
func (ans *webrtcSignalingAnswerer) answer(client webrtcpb.SignalingService_AnswerClient) (receivedInitRequest bool, err error) {
	receivedInitRequest = false

	resp, err := client.Recv()
	if err != nil {
		return receivedInitRequest, err
	}

	receivedInitRequest = true
	uuid := resp.Uuid
	initStage, ok := resp.Stage.(*webrtcpb.AnswerRequest_Init)
	if !ok {
		err := errors.Errorf("expected first stage to be init; got %T", resp.Stage)
		return receivedInitRequest, client.Send(&webrtcpb.AnswerResponse{
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
		return receivedInitRequest, client.Send(&webrtcpb.AnswerResponse{
			Uuid: uuid,
			Stage: &webrtcpb.AnswerResponse_Error{
				Error: &webrtcpb.AnswerResponseErrorStage{
					Status: ErrorToStatus(err).Proto(),
				},
			},
		})
	}

	serverChannel := ans.server.NewChannel(pc, dc, ans.hosts)

	var successful bool
	defer func() {
		if !(successful && err == nil) {
			var candPairStr string
			if candPair, hasCandPair := webrtcPeerConnCandPair(pc); hasCandPair {
				candPairStr = candPair.String()
			}

			connInfo := getWebRTCPeerConnectionStats(pc)
			iceConnectionState := pc.ICEConnectionState()
			iceGatheringState := pc.ICEGatheringState()
			ans.logger.Warnw("Connection establishment failed",
				"conn_id", connInfo.ID,
				"ice_connection_state", iceConnectionState,
				"ice_gathering_state", iceGatheringState,
				"conn_local_candidates", connInfo.LocalCandidates,
				"conn_remote_candidates", connInfo.RemoteCandidates,
				"candidate_pair", candPairStr,
			)

			// Close unhealthy connection.
			err = multierr.Combine(err, pc.GracefulClose())
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

	errCh := make(chan error)
	defer exchangeCancel()
	sendErr := func(err error) {
		if isEOF(err) {
			ans.logger.Warnf("answerer swallowing err %v", err)
			return
		}
		ans.logger.Warnf("answerer received err %v of type %T", err, err)
		select {
		case <-exchangeCtx.Done():
		case errCh <- err:
		}
	}

	initSent := make(chan struct{})
	if !init.OptionalConfig.DisableTrickle {
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			return receivedInitRequest, err
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
			ans.bgWorkersMu.RLock()
			ans.bgWorkers.Add(1)
			ans.bgWorkersMu.RUnlock()

			// Check if closeCtx has errored: underlying answerer may have been
			// `Stop`ped, in which case we mark this answer worker as `Done` and
			// return.
			if err := ans.closeCtx.Err(); err != nil {
				ans.bgWorkers.Done()
				return
			}

			utils.PanicCapturingGo(func() {
				defer ans.bgWorkers.Done()

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
			return receivedInitRequest, err
		}

		select {
		case <-exchangeCtx.Done():
			return receivedInitRequest, exchangeCtx.Err()
		case <-waitOneHost:
		}
	}

	encodedSDP, err := EncodeSDP(pc.LocalDescription())
	if err != nil {
		return receivedInitRequest, client.Send(&webrtcpb.AnswerResponse{
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
		return receivedInitRequest, err
	}
	close(initSent)

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
			sendErr(fmt.Errorf("%v", err))
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
		return receivedInitRequest, err
	}
	if err := sendDone(); err != nil {
		// Errors from sendDone (such as EOF) are sometimes caused by the signaling
		// server "ending" the exchange process earlier than the answerer due to
		// the caller being able to establish a connection without all the
		// answerer's ICE candidates (trickle ICE). Only Warn the error here to
		// avoid accidentally Closing a healthy, established peer connection.
		ans.logger.Warnw("error ending signaling exchange from answer client", "error", err)
	}
	successful = true
	return receivedInitRequest, nil
}
