package rpc

// TODO(RSDK-8666): use stoppable workers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/viamrobotics/webrtc/v3"
	"google.golang.org/grpc"

	"go.viam.com/utils"
)

// DefaultWebRTCMaxGRPCCalls is the maximum number of concurrent gRPC calls to allow
// for a server.
var DefaultWebRTCMaxGRPCCalls = 256

// A webrtcServer translates gRPC frames over WebRTC data channels into gRPC calls.
type webrtcServer struct {
	handlers map[string]handlerFunc
	services map[string]*serviceInfo
	logger   utils.ZapCompatibleLogger

	peerConnsMu sync.Mutex
	peerConns   map[*webrtc.PeerConnection]struct{}

	processHeadersWorkers *utils.StoppableWorkers

	callTickets chan struct{}

	unaryInt          grpc.UnaryServerInterceptor
	streamInt         grpc.StreamServerInterceptor
	unknownStreamDesc *grpc.StreamDesc

	onPeerAdded   func(pc *webrtc.PeerConnection)
	onPeerRemoved func(pc *webrtc.PeerConnection)

	counters struct {
		PeersConnected       atomic.Int64
		PeersDisconnected    atomic.Int64
		PeerConnectionErrors atomic.Int64
		HeadersProcessed     atomic.Int64

		// TotalTimeConnectingMillis just counts successful connection attempts.
		TotalTimeConnectingMillis atomic.Int64
	}
}

// WebRTCGrpcStats are stats of the webrtc variety.
type WebRTCGrpcStats struct {
	PeersConnected            int64
	PeersDisconnected         int64
	PeerConnectionErrors      int64
	PeersActive               int64
	HeadersProcessed          int64
	CallTicketsAvailable      int32
	TotalTimeConnectingMillis int64

	// When the FTDC frontend is more feature rich, we can remove this and let the frontend compute
	// the value.
	AverageTimeConnectingMillis float64
}

// Stats returns stats.
func (srv *webrtcServer) Stats() WebRTCGrpcStats {
	ret := WebRTCGrpcStats{
		PeersConnected:            srv.counters.PeersConnected.Load(),
		PeersDisconnected:         srv.counters.PeersDisconnected.Load(),
		PeerConnectionErrors:      srv.counters.PeerConnectionErrors.Load(),
		HeadersProcessed:          srv.counters.HeadersProcessed.Load(),
		CallTicketsAvailable:      int32(cap(srv.callTickets) - len(srv.callTickets)),
		TotalTimeConnectingMillis: srv.counters.TotalTimeConnectingMillis.Load(),
	}
	ret.PeersActive = ret.PeersConnected - ret.PeersDisconnected
	if ret.PeersConnected > 0 {
		ret.AverageTimeConnectingMillis = float64(ret.TotalTimeConnectingMillis) / float64(ret.PeersConnected)
	}

	return ret
}

// from grpc.
type serviceInfo struct {
	methods  map[string]*grpc.MethodDesc
	streams  map[string]*grpc.StreamDesc
	metadata interface{}
}

// newWebRTCServer makes a new server with no registered services.
func newWebRTCServer(logger utils.ZapCompatibleLogger) *webrtcServer {
	return newWebRTCServerWithInterceptors(logger, nil, nil)
}

// newWebRTCServerWithInterceptors makes a new server with no registered services that will
// use the given interceptors.
func newWebRTCServerWithInterceptors(
	logger utils.ZapCompatibleLogger,
	unaryInt grpc.UnaryServerInterceptor,
	streamInt grpc.StreamServerInterceptor,
) *webrtcServer {
	return newWebRTCServerWithInterceptorsAndUnknownStreamHandler(logger, unaryInt, streamInt, nil)
}

// newWebRTCServerWithInterceptorsAndUnknownStreamHandler makes a new server with no registered services that will
// use the given interceptors and unknown stream handler.
func newWebRTCServerWithInterceptorsAndUnknownStreamHandler(
	logger utils.ZapCompatibleLogger,
	unaryInt grpc.UnaryServerInterceptor,
	streamInt grpc.StreamServerInterceptor,
	unknownStreamDesc *grpc.StreamDesc,
) *webrtcServer {
	srv := &webrtcServer{
		handlers:          map[string]handlerFunc{},
		services:          map[string]*serviceInfo{},
		logger:            logger,
		peerConns:         map[*webrtc.PeerConnection]struct{}{},
		callTickets:       make(chan struct{}, DefaultWebRTCMaxGRPCCalls),
		unaryInt:          unaryInt,
		streamInt:         streamInt,
		unknownStreamDesc: unknownStreamDesc,
	}
	srv.processHeadersWorkers = utils.NewBackgroundStoppableWorkers()
	return srv
}

// Stop instructs the server and all handlers to stop. It returns when all handlers
// are done executing.
func (srv *webrtcServer) Stop() {
	srv.logger.Info("waiting for handlers to complete")
	srv.processHeadersWorkers.Stop()
	srv.logger.Info("handlers complete")
	srv.logger.Info("closing lingering peer connections")

	// Only lock to grab reference to peerConns. Locking while closing
	// connections can cause hangs (see RSDK-8789).
	srv.peerConnsMu.Lock()
	// Build a slice of peer conn references, as iterating through the map
	// without a lock will race with `removePeer`.
	var peerConns []*webrtc.PeerConnection
	for pc := range srv.peerConns {
		peerConns = append(peerConns, pc)
	}
	srv.peerConnsMu.Unlock()

	for _, pc := range peerConns {
		srv.counters.PeersDisconnected.Add(1)
		if err := pc.GracefulClose(); err != nil {
			srv.logger.Errorw("error closing peer connection", "error", err)
		}
	}
	srv.logger.Info("lingering peer connections closed")
}

// RegisterService registers the given implementation of a service to be handled via
// WebRTC data channels. It extracts the unary and stream methods from a service description
// and calls the methods on the implementation when requested via a data channel.
func (srv *webrtcServer) RegisterService(sd *grpc.ServiceDesc, ss interface{}) {
	info := &serviceInfo{
		methods:  make(map[string]*grpc.MethodDesc, len(sd.Methods)),
		streams:  make(map[string]*grpc.StreamDesc, len(sd.Streams)),
		metadata: sd.Metadata,
	}
	for i := range sd.Methods {
		d := &sd.Methods[i]
		info.methods[d.MethodName] = d
	}
	for i := range sd.Streams {
		d := &sd.Streams[i]
		info.streams[d.StreamName] = d
	}

	for i := range sd.Methods {
		desc := &sd.Methods[i]
		info.methods[desc.MethodName] = desc
		path := fmt.Sprintf("/%v/%v", sd.ServiceName, desc.MethodName)
		srv.handlers[path] = srv.unaryHandler(ss, methodHandler(desc.Handler))
	}
	for i := range sd.Streams {
		desc := &sd.Streams[i]
		info.streams[desc.StreamName] = desc
		path := fmt.Sprintf("/%v/%v", sd.ServiceName, desc.StreamName)
		srv.handlers[path] = srv.streamHandler(ss, path, *desc)
	}

	srv.services[sd.ServiceName] = info
}

func (srv *webrtcServer) GetServiceInfo() map[string]grpc.ServiceInfo {
	info := make(map[string]grpc.ServiceInfo, len(srv.services))
	for name, svcInfo := range srv.services {
		methods := make([]grpc.MethodInfo, 0, len(svcInfo.methods)+len(svcInfo.streams))
		for m := range svcInfo.methods {
			methods = append(methods, grpc.MethodInfo{
				Name:           m,
				IsClientStream: false,
				IsServerStream: false,
			})
		}
		for m, d := range svcInfo.streams {
			methods = append(methods, grpc.MethodInfo{
				Name:           m,
				IsClientStream: d.ClientStreams,
				IsServerStream: d.ServerStreams,
			})
		}

		info[name] = grpc.ServiceInfo{
			Methods:  methods,
			Metadata: svcInfo.metadata,
		}
	}
	return info
}

func (srv *webrtcServer) handler(path string) (handlerFunc, bool) {
	h, ok := srv.handlers[path]
	return h, ok
}

// NewChannel binds the given data channel to be serviced as the server end of a gRPC
// connection.
func (srv *webrtcServer) NewChannel(
	peerConn *webrtc.PeerConnection,
	dataChannel *webrtc.DataChannel,
	authAudience []string,
) *webrtcServerChannel {
	serverCh := newWebRTCServerChannel(srv, peerConn, dataChannel, authAudience, srv.logger)
	srv.peerConnsMu.Lock()
	srv.peerConns[peerConn] = struct{}{}
	srv.peerConnsMu.Unlock()
	if srv.onPeerAdded != nil {
		srv.onPeerAdded(peerConn)
	}
	return serverCh
}

func (srv *webrtcServer) removePeer(peerConn *webrtc.PeerConnection) {
	srv.peerConnsMu.Lock()
	delete(srv.peerConns, peerConn)
	srv.peerConnsMu.Unlock()
	srv.counters.PeersDisconnected.Add(1)
	if srv.onPeerRemoved != nil {
		srv.onPeerRemoved(peerConn)
	}
	if err := peerConn.GracefulClose(); err != nil {
		srv.logger.Errorw("error closing peer connection on removal", "error", err)
	}
}

type (
	handlerFunc   func(s *webrtcServerStream) error
	methodHandler func(
		srv interface{},
		ctx context.Context,
		dec func(interface{}) error,
		interceptor grpc.UnaryServerInterceptor,
	) (interface{}, error)
)

func (srv *webrtcServer) unaryHandler(ss interface{}, handler methodHandler) handlerFunc {
	return func(s *webrtcServerStream) error {
		ctx := grpc.NewContextWithServerTransportStream(s.webrtcBaseStream.Context(), serverTransportStream{s})

		response, err := handler(ss, ctx, s.webrtcBaseStream.RecvMsg, srv.unaryInt)
		if err != nil {
			s.closeWithSendError(err)
			return err
		}

		err = s.SendMsg(response)
		if err != nil {
			// `ServerStream.SendMsg` closes itself on error.
			return err
		}

		s.closeWithSendError(nil)
		return nil
	}
}

func (srv *webrtcServer) streamHandler(ss interface{}, method string, desc grpc.StreamDesc) handlerFunc {
	return func(s *webrtcServerStream) error {
		ctx := grpc.NewContextWithServerTransportStream(s.webrtcBaseStream.Context(), serverTransportStream{s})
		wrappedStream := ctxWrappedServerStream{s, ctx}

		var err error
		if srv.streamInt == nil {
			err = desc.Handler(ss, wrappedStream)
		} else {
			info := &grpc.StreamServerInfo{
				FullMethod:     method,
				IsClientStream: desc.ClientStreams,
				IsServerStream: desc.ServerStreams,
			}
			err = srv.streamInt(ss, wrappedStream, info, desc.Handler)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		s.closeWithSendError(err)
		return nil
	}
}
