package rpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
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
	logger                  golog.Logger
}

func newBaseChannel(
	ctx context.Context,
	peerConn *webrtc.PeerConnection,
	dataChannel *webrtc.DataChannel,
	onPeerDone func(),
	logger golog.Logger,
) *webrtcBaseChannel {
	ctx, cancel := context.WithCancel(ctx)
	ch := &webrtcBaseChannel{
		peerConn:    peerConn,
		dataChannel: dataChannel,
		ctx:         ctx,
		cancel:      cancel,
		ready:       make(chan struct{}),
		logger:      logger.With("ch", dataChannel.ID()),
	}
	dataChannel.OnOpen(ch.onChannelOpen)
	dataChannel.OnClose(ch.onChannelClose)
	dataChannel.OnError(ch.onChannelError)

	var connID string
	var connIDMu sync.Mutex
	var peerDoneOnce bool
	doPeerDone := func() {
		if !peerDoneOnce && onPeerDone != nil {
			peerDoneOnce = true
			onPeerDone()
		}
	}
	connStateChanged := func(connectionState webrtc.ICEConnectionState) {
		ch.mu.Lock()
		if ch.closed {
			doPeerDone()
			ch.mu.Unlock()
			return
		}
		ch.activeBackgroundWorkers.Add(1)
		ch.mu.Unlock()

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
			case webrtc.ICEConnectionStateChecking, webrtc.ICEConnectionStateCompleted,
				webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateNew:
				fallthrough
			default:
				var candPair *webrtc.ICECandidatePair
				if connectionState == webrtc.ICEConnectionStateConnected && peerConn.SCTP() != nil &&
					peerConn.SCTP().Transport() != nil &&
					peerConn.SCTP().Transport().ICETransport() != nil {
					//nolint:errcheck
					candPair, _ = peerConn.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
				}
				connInfo := getWebRTCPeerConnectionStats(peerConn)
				connIDMu.Lock()
				connID = connInfo.ID
				connIDMu.Unlock()
				logger.Debugw("connection state changed",
					"conn_id", connInfo.ID,
					"conn_state", connectionState.String(),
					"conn_remote_candidates", connInfo.RemoteCandidates,
				)
				if candPair != nil {
					logger.Debugw("selected candidate pair",
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
	return ch.peerConn.Close()
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

func (ch *webrtcBaseChannel) onChannelError(err error) {
	ch.logger.Errorw("channel error", "error", err)
	if err := ch.closeWithReason(err); err != nil {
		ch.logger.Errorw("error closing channel", "error", err)
	}
}

const maxDataChannelSize = 16384

func (ch *webrtcBaseChannel) write(msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	if err := ch.dataChannel.Send(data); err != nil {
		if strings.Contains(err.Error(), "sending payload data in non-established state") {
			return io.ErrClosedPipe
		}
		return err
	}
	return nil
}
