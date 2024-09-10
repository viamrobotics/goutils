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
	defaultMaxAnswerers               = 2
	answererConnectTimeout            = 10 * time.Second
	answererConnectTimeoutBehindProxy = time.Minute
	answererReconnectWait             = time.Second
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

			timeout := answererConnectTimeout
			// Bump timeout from 10 seconds to 1 minute if behind a SOCKS proxy. It
			// may take longer to connect to the signaling server in that case.
			if proxyAddr := os.Getenv(socksProxyEnvVar); proxyAddr != "" {
				timeout = answererConnectTimeoutBehindProxy
			}
			setupCtx, timeoutCancel := context.WithTimeout(ans.closeCtx, timeout)
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

			// Create an `answerAttempt` to take advantage of the `sendError` method for the
			// upcoming type check.
			aa := &answerAttempt{
				webrtcSignalingAnswerer: ans,
				uuid:                    incomingCallerReq.Uuid,
				client:                  client,
				trickleEnabled:          true,
			}

			initStage, ok := incomingCallerReq.Stage.(*webrtcpb.AnswerRequest_Init)
			if !ok {
				aa.sendError(fmt.Errorf("expected first stage to be init; got %T", incomingCallerReq.Stage))
				ans.logger.Warnw("error communicating with signaling server", "error", err)
				continue
			}

			if cfg := initStage.Init.OptionalConfig; cfg != nil && cfg.DisableTrickle {
				aa.trickleEnabled = false
			}
			aa.offerSDP = initStage.Init.Sdp

			var answerCtx context.Context
			var answerCtxCancel func()
			if deadline := initStage.Init.Deadline; deadline != nil {
				answerCtx, answerCtxCancel = context.WithDeadline(aa.closeCtx, deadline.AsTime())
			} else {
				answerCtx, answerCtxCancel = context.WithTimeout(aa.closeCtx, getDefaultOfferDeadline())
			}

			if err = aa.connect(answerCtx); err != nil {
				answerCtxCancel()
				// We received an error while trying to connect to a caller/peer.
				ans.logger.Errorw("error connecting to peer", "error", err)
				if !utils.SelectContextOrWait(ans.closeCtx, answererReconnectWait) {
					return
				}
			}
			answerCtxCancel()
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
	// The uuid is the key for communicating with the signaling server about this connection
	// attempt.
	uuid   string
	client webrtcpb.SignalingService_AnswerClient

	trickleEnabled bool
	offerSDP       string

	// When a connection attempt concludes, either with success or failure, we will fire a single
	// message to the signaling server. This allows the signaling server to release resources
	// related to this connection attempt.
	sendDoneErrOnce sync.Once
}

// connect accepts a single call offer, responds with a corresponding SDP, and
// attempts to establish a WebRTC connection with the caller via ICE. Once established,
// the designated WebRTC data channel is passed off to the underlying Server which
// is then used as the server end of a gRPC connection.
func (aa *answerAttempt) connect(ctx context.Context) (err error) {
	// If SOCKS proxy is indicated by environment, extend WebRTC config with an
	// `OptionalWebRTCConfig` call to the signaling server. The usage of a SOCKS
	// proxy indicates that the server may need a local TURN ICE candidate to
	// make a conntion to any peer. Nomination of that type of candidate is only
	// possible through extending the WebRTC config with a TURN URL (and
	// associated username and password).
	webrtcConfig := aa.webrtcConfig
	if proxyAddr := os.Getenv(socksProxyEnvVar); proxyAddr != "" {
		aa.logger.Info("behind SOCKS proxy; extending WebRTC config with TURN URL")
		aa.connMu.Lock()
		conn := aa.conn
		aa.connMu.Unlock()

		// Use first host on answerer for rpc-host field in metadata.
		signalingClient := webrtcpb.NewSignalingServiceClient(conn)
		md := metadata.New(map[string]string{RPCHostMetadataField: aa.hosts[0]})

		signalCtx := metadata.NewOutgoingContext(ctx, md)
		configResp, err := signalingClient.OptionalWebRTCConfig(signalCtx,
			&webrtcpb.OptionalWebRTCConfigRequest{})
		if err != nil {
			// Any error below indicates the signaling server is not present.
			if s, ok := status.FromError(err); ok && (s.Code() == codes.Unimplemented ||
				(s.Code() == codes.InvalidArgument && s.Message() == hostNotAllowedMsg)) {
				return ErrNoWebRTCSignaler
			}
			return err
		}
		webrtcConfig = extendWebRTCConfig(&webrtcConfig, configResp.Config, true)
		aa.logger.Debugw("extended WebRTC config", "ICE servers", webrtcConfig.ICEServers)
	}

	pc, dc, err := newPeerConnectionForServer(
		ctx,
		aa.offerSDP,
		webrtcConfig,
		!aa.trickleEnabled,
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

	initSent := make(chan struct{})
	if aa.trickleEnabled {
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			return err
		}

		var pendingCandidates sync.WaitGroup
		waitOneHost := make(chan struct{})
		var waitOneHostOnce sync.Once
		pc.OnICECandidate(func(icecandidate *webrtc.ICECandidate) {
			if ctx.Err() != nil {
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
				case <-ctx.Done():
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
		case <-waitOneHost:
			// Dan: We wait for one host before proceeding to ensure the initial response has some
			// candidate information. This is a Nagle's algorithm-esque batching optimization. I
			// think.
		case <-ctx.Done():
			return ctx.Err()
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

	if aa.trickleEnabled {
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
						aa.logger.Warn("Error receiving initial message from signaling server", "err", err)
					}
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
	case <-ctx.Done():
		// Timed out or signaling server was closed.
		aa.sendError(multierr.Combine(ctx.Err(), serverChannel.Close()))
		return ctx.Err()
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

		if sendErr != nil {
			// Errors communicating with the signaling server have no bearing on whether the
			// PeerConnection is usable. Log and ignore the send error.
			aa.logger.Warnw("Failed to send connection success message to signaling server", "sendErr", sendErr)
		}
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

		if sendErr != nil {
			aa.logger.Warnw("Failed to send error message to signaling server", "sendErr", sendErr)
		}
	})
}
