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

	// No lock is necessary here. It is illegal to call `ans.Stop` before `ans.Start` returns.
	ans.bgWorkers.Add(1)

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

	// The answerer may be stopped (canceling the context and waiting on background workers)
	// concurrently to executing the below code. In that circumstance we must guarantee either:
	// * `Stop` waiting on the `bgWorkers` WaitGroup observes our `bgWorkers.Add` or
	// * Our code observes `Stop`s closing of the `closeCtx`
	//
	// We use a mutex to make the read of the `closeCtx` and write to the `bgWorkers` atomic. `Stop`
	// takes a competing mutex around canceling the `closeCtx`.
	ans.bgWorkersMu.RLock()
	select {
	case <-ans.closeCtx.Done():
		ans.bgWorkersMu.RUnlock()
		return
	default:
	}
	ans.bgWorkers.Add(1)
	ans.bgWorkersMu.RUnlock()

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
			if err != nil {
				if checkExceptionalError(err) != nil {
					ans.logger.Warnw("error communicating with signaling server", "error", err)
					if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
						return
					}
				}
				continue
			}

			// `client.Recv` waits, typically for a long time, for a caller to show up. Which is
			// when the signaling server will send a response saying someone wants to connect.
			incomingCallerReq, err := client.Recv()
			if err != nil {
				if checkExceptionalError(err) != nil {
					ans.logger.Warnw("error communicating with signaling server", "error", err)
					if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
						return
					}
				}
				continue
			}

			aa := &answerAttempt{
				webrtcSignalingAnswerer: ans,
				uuid:                    incomingCallerReq.Uuid,
				client:                  client,
			}
			if err = aa.connect(incomingCallerReq); err != nil {
				// We received an error while trying to connect to a caller/peer.
				ans.logger.Errorw("error connecting to peer", "error", err)
				if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
					return
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

	// Code adding workers must atomically check the `closeCtx` before adding to the `bgWorkers`
	// wait group. Canceling the context must not split those two operations. We ensure this
	// atomicity by acquiring the `bgWorkersMu` write lock.
	ans.bgWorkersMu.Lock()
	ans.cancelBgWorkers()
	// Background workers require the `bgWorkersMu`. Release the mutex before calling `Wait`.
	ans.bgWorkersMu.Unlock()
	ans.bgWorkers.Wait()

	ans.connMu.Lock()
	defer ans.connMu.Unlock()
	if ans.conn != nil {
		if err := checkExceptionalError(ans.conn.Close()); err != nil {
			ans.logger.Errorw("error closing signaling connection", "error", err)
		}
		ans.conn = nil
	}
}

type answerAttempt struct {
	*webrtcSignalingAnswerer
	uuid   string
	client webrtcpb.SignalingService_AnswerClient

	sendDoneErrOnce sync.Once
}

// connect accepts a single call offer, responds with a corresponding SDP, and
// attempts to establish a WebRTC connection with the caller via ICE. Once established,
// the designated WebRTC data channel is passed off to the underlying Server which
// is then used as the server end of a gRPC connection.
func (aa *answerAttempt) connect(req *webrtcpb.AnswerRequest) (err error) {
	initStage, ok := req.Stage.(*webrtcpb.AnswerRequest_Init)
	if !ok {
		aa.sendError(fmt.Errorf("expected first stage to be init; got %T", req.Stage))
		return err
	}
	init := initStage.Init

	disableTrickle := false
	if init.OptionalConfig != nil {
		disableTrickle = init.OptionalConfig.DisableTrickle
	}
	pc, dc, err := newPeerConnectionForServer(
		aa.closeCtx,
		init.Sdp,
		aa.webrtcConfig,
		disableTrickle,
		aa.logger,
	)
	if err != nil {
		aa.sendError(err)
		return err
	}

	// We have a PeerConnection object. Install an error handler.
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
			aa.logger.Warnw("Connection establishment failed",
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

	serverChannel := aa.server.NewChannel(pc, dc, aa.hosts)

	var exchangeCtx context.Context
	var exchangeCancel func()
	if initStage.Init.Deadline != nil {
		exchangeCtx, exchangeCancel = context.WithDeadline(aa.closeCtx, initStage.Init.Deadline.AsTime())
	} else {
		exchangeCtx, exchangeCancel = context.WithTimeout(aa.closeCtx, getDefaultOfferDeadline())
	}
	defer exchangeCancel()
	errCh := make(chan error)

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

			// The answerer may be stopped (canceling the context and waiting on background workers)
			// concurrently to executing the below code. In that circumstance we must guarantee
			// either:
			// * `Stop` waiting on the `bgWorkers` WaitGroup observes our `bgWorkers.Add` or
			// * Our code observes `Stop`s closing of the `closeCtx`
			//
			// We use a mutex to make the read of the `closeCtx` and write to the `bgWorkers`
			// atomic. `Stop` takes a competing mutex around canceling the `closeCtx`.
			aa.bgWorkersMu.RLock()
			select {
			case <-aa.closeCtx.Done():
				aa.bgWorkersMu.RUnlock()
				return
			default:
			}
			aa.bgWorkers.Add(1)
			aa.bgWorkersMu.RUnlock()

			// must spin off to unblock the ICE gatherer
			utils.PanicCapturingGo(func() {
				defer aa.bgWorkers.Done()

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
						aa.logger.Debug("Sleeping to delay the end of the negotiation")
						time.Sleep(1 * time.Second)
					}
					pendingCandidates.Wait()
					aa.sendDone()
					return
				}
				iProto := iceCandidateToProto(icecandidate)
				if err := aa.client.Send(&webrtcpb.AnswerResponse{
					Uuid: aa.uuid,
					Stage: &webrtcpb.AnswerResponse_Update{
						Update: &webrtcpb.AnswerResponseUpdateStage{
							Candidate: iProto,
						},
					},
				}); err != nil {
					aa.sendError(err)
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

	encodedSDP, err := EncodeSDP(pc.LocalDescription())
	if err != nil {
		aa.sendError(err)
		return err
	}

	if err := aa.client.Send(&webrtcpb.AnswerResponse{
		Uuid: aa.uuid,
		Stage: &webrtcpb.AnswerResponse_Init{
			Init: &webrtcpb.AnswerResponseInitStage{
				Sdp: encodedSDP,
			},
		},
	}); err != nil {
		return err
	}
	close(initSent)

	if !init.OptionalConfig.DisableTrickle {
		done := make(chan struct{})
		defer func() { <-done }()

		utils.PanicCapturingGoWithCallback(func() {
			defer close(done)

			for {
				// `client` was constructed based off of the `ans.closeCtx`. We rely on the
				// underlying `client.Recv` implementation checking that context for cancelation.
				ansResp, err := aa.client.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						return
					}

					// Dan: ContextCanceled case?
					return
				}

				switch stage := ansResp.Stage.(type) {
				case *webrtcpb.AnswerRequest_Init:
				case *webrtcpb.AnswerRequest_Update:
					if ansResp.Uuid != aa.uuid {
						aa.sendError(fmt.Errorf("uuid mismatch; have=%q want=%q", ansResp.Uuid, aa.uuid))
						return
					}
					cand := iceCandidateFromProto(stage.Update.Candidate)
					if err := pc.AddICECandidate(cand); err != nil {
						aa.sendError(err)
						return
					}
				case *webrtcpb.AnswerRequest_Done:
					return
				case *webrtcpb.AnswerRequest_Error:
					respStatus := status.FromProto(stage.Error.Status)
					aa.sendError(fmt.Errorf("error from requester: %w", respStatus.Err()))
					return
				default:
					aa.sendError(fmt.Errorf("unexpected stage %T", stage))
					return
				}
			}
		}, func(err interface{}) {
			aa.sendError(fmt.Errorf("%v", err))
		})
	}

	select {
	case <-serverChannel.Ready():
		// Happy path
		successful = true
	case <-exchangeCtx.Done():
		aa.sendError(multierr.Combine(exchangeCtx.Err(), serverChannel.Close()))
		return exchangeCtx.Err()
	case err := <-errCh:
		aa.sendError(multierr.Combine(err, serverChannel.Close()))
		return err
	}

	aa.sendDone()
	return nil
}

func (aa *answerAttempt) sendDone() {
	aa.sendDoneErrOnce.Do(func() {
		sendErr := aa.client.Send(&webrtcpb.AnswerResponse{
			Uuid: aa.uuid,
			Stage: &webrtcpb.AnswerResponse_Done{
				Done: &webrtcpb.AnswerResponseDoneStage{},
			},
		})

		// Errors from sendDone (such as EOF) are sometimes caused by the signaling server "ending"
		// the exchange process earlier than the answerer due to the caller being able to establish
		// a connection without all the answerer's ICE candidates (trickle ICE). Only Warn the error
		// here to avoid accidentally Closing a healthy, established peer connection.
		aa.logger.Warnw("Error sending DoneMessage to signaling server", "sendErr", sendErr)
	})
}

func (aa *answerAttempt) sendError(err error) {
	aa.sendDoneErrOnce.Do(func() {
		sendErr := aa.client.Send(&webrtcpb.AnswerResponse{
			Uuid: aa.uuid,
			Stage: &webrtcpb.AnswerResponse_Error{
				Error: &webrtcpb.AnswerResponseErrorStage{
					Status: ErrorToStatus(err).Proto(),
				},
			},
		})

		aa.logger.Warnw("Error sending ErrorMessage to signaling server", "sendErr", sendErr)
	})
}
