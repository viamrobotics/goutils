package rpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
	"github.com/pion/webrtc/v4"
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
	closed                  bool
	closedReason            error
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
	dataChannel.OnClose(ch.onChannelClose)
	dataChannel.OnError(ch.onChannelError)
	dataChannel.SetBufferedAmountLowThreshold(bufferThreshold)
	dataChannel.OnBufferedAmountLow(func() {
		ch.bufferWriteMu.Lock()
		ch.bufferWriteCond.Broadcast()
		ch.bufferWriteMu.Unlock()
	})

	var connID string
	var connIDMu sync.Mutex
	var peerDoneOnce bool
	doPeerDone := func() {
		// Cancel base channel context so streams on the channel will stop trying
		// to receive messages when the peer is done.
		ch.cancel()

		if !peerDoneOnce && onPeerDone != nil {
			peerDoneOnce = true
			onPeerDone()
		}
	}
	connStateChanged := func(connectionState webrtc.ICEConnectionState) {
		ch.activeBackgroundWorkers.Add(1)
		utils.PanicCapturingGo(func() {
			defer ch.activeBackgroundWorkers.Done()

			ch.mu.Lock()
			defer ch.mu.Unlock()
			if ch.closed {
				doPeerDone()
				return
			}

			switch connectionState {
			case webrtc.ICEConnectionStateDisconnected,
				webrtc.ICEConnectionStateFailed,
				webrtc.ICEConnectionStateClosed:
				connIDMu.Lock()
				currConnID := connID
				connIDMu.Unlock()
				if currConnID == "" { // make sure we've gathered information before
					return
				}
				logger.Debugw("connection state changed",
					"conn_id", currConnID,
					"conn_state", connectionState.String(),
				)
				doPeerDone()
				// The `Disconnected` state change implies the other side has closed the peer
				// connection. Despite learning the other side has gone away, pion does not close
				// its internal resources. Notably, things like `TrackRemote.ReadRTP` can hang. We'd
				// instead prefer for that to receive an EOF, so it can close normally. Therefore
				// upon reaching the `Disconnected` state, we explicitly call `Close` on our side of
				// the `PeerConnection`.
				//
				// We chose here to call close for all cases of `Disconnected`, `Failed` and
				// `Closed`. We rely on pion's `PeerConnection.GracefulClose` method being idempotent.
				// We "gracefully" close to wait for the read loop to complete.
				if err := peerConn.GracefulClose(); err != nil {
					logger.Debugw("Error closing peer connection",
						"conn_id", currConnID,
						"conn_state", connectionState.String(),
						"err", err,
					)
				}
			case webrtc.ICEConnectionStateConnected:
				if onICEConnected != nil {
					onICEConnected()
				}
				fallthrough
			case webrtc.ICEConnectionStateChecking, webrtc.ICEConnectionStateCompleted,
				webrtc.ICEConnectionStateNew, webrtc.ICEConnectionStateUnknown:
				fallthrough
			default:
				candPair, hasCandPair := webrtcPeerConnCandPair(peerConn)
				connInfo := getWebRTCPeerConnectionStats(peerConn)
				connIDMu.Lock()
				connID = connInfo.ID
				connIDMu.Unlock()
				logger.Infow("Connection state changed",
					"conn_id", connInfo.ID,
					"conn_state", connectionState.String(),
					"conn_local_candidates", connInfo.LocalCandidates,
					"conn_remote_candidates", connInfo.RemoteCandidates,
				)
				if hasCandPair {
					logger.Infow("Selected candidate pair",
						"conn_id", connInfo.ID,
						"candidate_pair", candPair.String(),
					)
				}
			}
		})
	}
	peerConn.OnICEConnectionStateChange(connStateChanged)

	// fire once
	connStateChanged(peerConn.ICEConnectionState())

	return ch
}

func (ch *webrtcBaseChannel) closeWithReason(err error) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return nil
	}
	ch.closed = true
	ch.closedReason = err
	ch.cancel()
	ch.bufferWriteCond.Broadcast()

	// Underlying connection may already be closed; ignore "conn is closed"
	// errors.
	if err := ch.peerConn.GracefulClose(); !errors.Is(err, dtls.ErrConnClosed) {
		return err
	}
	return nil
}

func (ch *webrtcBaseChannel) Close() error {
	defer ch.activeBackgroundWorkers.Wait()
	return ch.closeWithReason(nil)
}

func (ch *webrtcBaseChannel) Closed() (bool, error) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.closed, ch.closedReason
}

func (ch *webrtcBaseChannel) Ready() <-chan struct{} {
	return ch.ready
}

func (ch *webrtcBaseChannel) onChannelOpen() {
	close(ch.ready)
}

var errDataChannelClosed = errors.New("data channel closed")

func (ch *webrtcBaseChannel) onChannelClose() {
	if err := ch.closeWithReason(errDataChannelClosed); err != nil {
		ch.logger.Errorw("error closing channel", "error", err)
	}
}

// isUserInitiatedAbortChunkErr returns true if the error is an abort chunk
// error that the user initiated through Close. Certain browsers (Safari,
// Chrome and potentially others) close RTCPeerConnections with this type of
// abort chunk that is not indicative of an actual state of error.
func isUserInitiatedAbortChunkErr(err error) bool {
	return err != nil && errors.Is(err, sctp.ErrChunk) &&
		strings.Contains(err.Error(), "User Initiated Abort:")
}

func (ch *webrtcBaseChannel) onChannelError(err error) {
	if errors.Is(err, sctp.ErrResetPacketInStateNotExist) ||
		isUserInitiatedAbortChunkErr(err) {
		return
	}
	ch.logger.Errorw("channel error", "error", err)
	if err := ch.closeWithReason(err); err != nil {
		ch.logger.Errorw("error closing channel", "error", err)
	}
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
			return io.ErrClosedPipe
		}
		if ch.dataChannel.BufferedAmount() >= bufferThreshold {
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
