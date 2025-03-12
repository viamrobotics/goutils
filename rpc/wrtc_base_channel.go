package rpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pion/sctp"
	"github.com/viamrobotics/webrtc/v3"
	"google.golang.org/protobuf/proto"

	"go.viam.com/utils"
)

type webrtcBaseChannel struct {
	mu                      sync.Mutex
	peerConn                *webrtc.PeerConnection
	dataChannel             *webrtc.DataChannel
	ctx                     context.Context
	cancel                  func()
	ready                   chan struct{}
	closed                  atomic.Bool
	activeBackgroundWorkers sync.WaitGroup
	logger                  utils.ZapCompatibleLogger
	bufferWriteMu           sync.RWMutex
	bufferWriteCond         *sync.Cond
}

const bufferThreshold = 1024 * 1024

func newBaseChannel(
	ctx context.Context,
	peerConn *webrtc.PeerConnection,
	dataChannel *webrtc.DataChannel,
	onPeerDone func(),
	onICEConnected func(),
	logger utils.ZapCompatibleLogger,
) *webrtcBaseChannel {
	ctx, cancel := context.WithCancel(ctx)
	ch := &webrtcBaseChannel{
		peerConn:    peerConn,
		dataChannel: dataChannel,
		ctx:         ctx,
		cancel:      cancel,
		ready:       make(chan struct{}),
		logger:      utils.AddFieldsToLogger(logger, "ch", dataChannel.ID()),
	}
	ch.bufferWriteCond = sync.NewCond(ch.bufferWriteMu.RLocker())
	dataChannel.OnOpen(ch.onChannelOpen)
	dataChannel.OnClose(ch.Close)
	dataChannel.OnError(ch.onChannelError)
	dataChannel.SetBufferedAmountLowThreshold(bufferThreshold)
	dataChannel.OnBufferedAmountLow(func() {
		ch.bufferWriteMu.Lock()
		ch.bufferWriteCond.Broadcast()
		ch.bufferWriteMu.Unlock()
	})

	var connID string
	var connIDMu sync.Mutex
	var peerDoneOnce sync.Once
	peerConn.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		if ch.closed.Load() {
			return
		}

		switch connectionState {
		case webrtc.ICEConnectionStateDisconnected,
			webrtc.ICEConnectionStateFailed,
			webrtc.ICEConnectionStateClosed:

			// The Disconnected/Failed/Closed states are all interpreted as terminal. In theory,
			// WebRTC allows for moving from the disconnected -> connected state if the network
			// heals or new candidates are presented. We forgo that optimization of preserving an
			// existing PeerConnection object and instead opt for clients to redial and perform a
			// new round of signaling/answering.
			connIDMu.Lock()
			currConnID := connID
			connIDMu.Unlock()
			if currConnID == "" { // make sure we've gathered information before
				return
			}

			if connectionState == webrtc.ICEConnectionStateDisconnected {
				// Disconnections happen when the client and server are no longer communicating. But
				// neither side has closed the peer connection. These events can happen in more
				// interesting states than typical shutdown (e.g: there may be a bunch of data that
				// has been queued up to send due to the network problem) such that we feel it's
				// warranted to log at a higher level.
				logger.Warnw("connection state changed",
					"conn_id", currConnID,
					"conn_state", connectionState.String(),
				)
			} else {
				logger.Debugw("connection state changed",
					"conn_id", currConnID,
					"conn_state", connectionState.String(),
				)
			}

			// We will close+wait on all of the channel related resources. We will additionally
			// close the PeerConnection, but not wait on that to drain its resources. Any desired
			// PeerConnection waiting will be performed by the client connection object or server
			// object.
			ch.Close()
			peerDoneOnce.Do(func() {
				if onPeerDone != nil {
					onPeerDone()
				}
			})
		case webrtc.ICEConnectionStateConnected:
			if onICEConnected != nil {
				// The user of `onICEConnected` waits for a few seconds before enumerating/logging
				// candidates. Spin off a goroutine to avoid consequences of sleeping.
				ch.activeBackgroundWorkers.Add(1)
				go func() {
					defer ch.activeBackgroundWorkers.Done()
					onICEConnected()
				}()
			}
			fallthrough
		case webrtc.ICEConnectionStateChecking, webrtc.ICEConnectionStateCompleted,
			webrtc.ICEConnectionStateNew:
			fallthrough
		default:
			candPair, hasCandPair := webrtcPeerConnCandPair(peerConn)
			connInfo := getWebRTCPeerConnectionStats(peerConn)
			connIDMu.Lock()
			connID = connInfo.ID
			connIDMu.Unlock()
			connectionStateChangedLogFields := []interface{}{
				"conn_id", connInfo.ID,
				"conn_local_candidates", connInfo.LocalCandidates,
				"conn_remote_candidates", connInfo.RemoteCandidates,
			}
			if hasCandPair {
				// Use info level when there is a selected candidate pair, as a
				// connection has been established.
				connectionStateChangedLogFields = append(connectionStateChangedLogFields,
					"candidate_pair", candPair.String())
				logger.Infow("Connection establishment succeeded", connectionStateChangedLogFields...)
			} else {
				// Use debug level when there is no selected candidate pair to avoid
				// noise.
				connectionStateChangedLogFields = append(connectionStateChangedLogFields,
					"conn_state", connectionState.String())
				logger.Debugw("Connection state changed", connectionStateChangedLogFields...)
			}
		}
	})

	return ch
}

// Close will always wait for background goroutines to exit before returning. It is safe to
// concurrently call `Close`.
//
// RSDK-8941: The above is a statement of expectations from existing code. Not a claim it is
// factually correct.
func (ch *webrtcBaseChannel) Close() {
	// RSDK-8941: Having this instead early return when `closed` is set will result in `TestServer`
	// to leak goroutines created by `dialWebRTC`.
	ch.closed.CompareAndSwap(false, true)

	ch.mu.Lock()
	// APP-6839: We must hold the `bufferWriteMu` to avoid a "missed notification" that can happen
	// when a `webrtcBaseChannel.write` happens concurrently with `closeWithReason`. Specifically,
	// this lock makes atomic the `ch.cancel` with the broadcast. Such that a call to write that can
	// `Wait` on this condition variable must either:
	// - Observe the context being canceled, or
	// - Call `Wait` before* the following `Broadcast` is invoked.
	ch.bufferWriteMu.Lock()
	ch.cancel()
	ch.bufferWriteCond.Broadcast()
	ch.bufferWriteMu.Unlock()
	ch.mu.Unlock()

	// We use `PeerConnection.Close` here rather than `GracefulClose`. The data channel is owned by
	// the PeerConnection. Let's not have the data channel wait for the peer connection to
	// completely clean up. We only wish to ensure that no requests/response (i.e:
	// webrtc[Server/Client]Streams) are in operation.
	utils.UncheckedError(ch.peerConn.Close())
	ch.activeBackgroundWorkers.Wait()
}

func (ch *webrtcBaseChannel) Closed() bool {
	return ch.closed.Load()
}

func (ch *webrtcBaseChannel) Ready() <-chan struct{} {
	return ch.ready
}

func (ch *webrtcBaseChannel) onChannelOpen() {
	close(ch.ready)
}

// isUserInitiatedAbortChunkErr returns true if the error is an abort chunk
// error that the user initiated through Close. Certain browsers (Safari,
// Chrome and potentially others) close RTCPeerConnections with this type of
// abort chunk that is not indicative of an actual state of error.
func isUserInitiatedAbortChunkErr(err error) bool {
	return err != nil && errors.Is(err, sctp.ErrChunk) &&
		strings.Contains(err.Error(), "User Initiated Abort: Close called")
}

func (ch *webrtcBaseChannel) onChannelError(err error) {
	if errors.Is(err, sctp.ErrResetPacketInStateNotExist) ||
		isUserInitiatedAbortChunkErr(err) {
		return
	}
	ch.logger.Errorw("channel error", "error", err)
	ch.Close()
}

const maxDataChannelSize = 65535

func (ch *webrtcBaseChannel) write(msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	ch.bufferWriteCond.L.Lock()
	for {
		if ch.ctx.Err() != nil {
			ch.bufferWriteCond.L.Unlock()
			return io.ErrClosedPipe
		}

		// RSDK-9239: Only wait when we're strictly over the threshold. Pion invokes the registered
		// callback (notify `bufferWriteCond`) when moving from larger than bufferThreshold to less
		// than or equal to.
		if ch.dataChannel.BufferedAmount() > bufferThreshold {
			ch.bufferWriteCond.Wait()
			continue
		}
		ch.bufferWriteCond.L.Unlock()
		break
	}
	if err := ch.dataChannel.Send(data); err != nil {
		if strings.Contains(err.Error(), "sending payload data in non-established state") {
			return io.ErrClosedPipe
		}
		return err
	}
	return nil
}
