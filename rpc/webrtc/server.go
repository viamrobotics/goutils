package rpcwebrtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
	"google.golang.org/grpc"
)

// DefaultMaxGRPCCalls is the maximum number of concurrent gRPC calls to allow
// for a server.
var DefaultMaxGRPCCalls = 256

// A Server translates gRPC frames over WebRTC data channels into gRPC calls.
type Server struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	handlers map[string]handlerFunc
	logger   golog.Logger

	peerConns               map[*webrtc.PeerConnection]struct{}
	activeBackgroundWorkers sync.WaitGroup
	callTickets             chan struct{}

	unaryInt  grpc.UnaryServerInterceptor
	streamInt grpc.StreamServerInterceptor
}

// NewServer makes a new server with no registered services.
func NewServer(logger golog.Logger) *Server {
	return NewServerWithInterceptors(logger, nil, nil)
}

// NewServerWithInterceptors makes a new server with no registered services that will
// use the given interceptors.
func NewServerWithInterceptors(
	logger golog.Logger,
	unaryInt grpc.UnaryServerInterceptor,
	streamInt grpc.StreamServerInterceptor,
) *Server {
	srv := &Server{
		handlers:    map[string]handlerFunc{},
		logger:      logger,
		peerConns:   map[*webrtc.PeerConnection]struct{}{},
		callTickets: make(chan struct{}, DefaultMaxGRPCCalls),
		unaryInt:    unaryInt,
		streamInt:   streamInt,
	}
	srv.ctx, srv.cancel = context.WithCancel(context.Background())
	return srv
}

// Stop instructs the server and all handlers to stop. It returns when all handlers
// are done executing.
func (srv *Server) Stop() {
	srv.cancel()
	srv.logger.Info("waiting for handlers to complete")
	srv.activeBackgroundWorkers.Wait()
	srv.logger.Info("handlers complete")
	srv.mu.Lock()
	srv.logger.Info("closing lingering peer connections")
	for pc := range srv.peerConns {
		if err := pc.Close(); err != nil {
			srv.logger.Errorw("error closing peer connection", "error", err)
		}
	}
	srv.logger.Info("lingering peer connections closed")
	srv.mu.Unlock()
}

// RegisterService registers the given implementation of a service to be handled via
// WebRTC data channels. It extracts the unary and stream methods from a service description
// and calls the methods on the implementation when requested via a data channel.
func (srv *Server) RegisterService(sd *grpc.ServiceDesc, ss interface{}) {
	for _, desc := range sd.Methods {
		path := fmt.Sprintf("/%v/%v", sd.ServiceName, desc.MethodName)
		srv.handlers[path] = srv.unaryHandler(ss, methodHandler(desc.Handler))
	}
	for _, desc := range sd.Streams {
		path := fmt.Sprintf("/%v/%v", sd.ServiceName, desc.StreamName)
		srv.handlers[path] = srv.streamHandler(ss, path, desc)
	}
}

func (srv *Server) handler(path string) (handlerFunc, bool) {
	h, ok := srv.handlers[path]
	return h, ok
}

// NewChannel binds the given data channel to be serviced as the server end of a gRPC
// connection.
func (srv *Server) NewChannel(peerConn *webrtc.PeerConnection, dataChannel *webrtc.DataChannel) *ServerChannel {
	serverCh := NewServerChannel(srv, peerConn, dataChannel, srv.logger)
	srv.mu.Lock()
	srv.peerConns[peerConn] = struct{}{}
	srv.mu.Unlock()
	return serverCh
}

func (srv *Server) removePeer(peerConn *webrtc.PeerConnection) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	delete(srv.peerConns, peerConn)
	if err := peerConn.Close(); err != nil {
		srv.logger.Errorw("error closing peer connection on removal", "error", err)
	}
}

type (
	handlerFunc   func(s *ServerStream) error
	methodHandler func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error)
)

func (srv *Server) unaryHandler(ss interface{}, handler methodHandler) handlerFunc {
	return func(s *ServerStream) error {
		response, err := handler(ss, s.ctx, s.RecvMsg, srv.unaryInt)
		if err != nil {
			return s.closeWithSendError(err)
		}
		return s.closeWithSendError(s.SendMsg(response))
	}
}

func (srv *Server) streamHandler(ss interface{}, method string, desc grpc.StreamDesc) handlerFunc {
	return func(s *ServerStream) error {
		var err error
		if srv.streamInt == nil {
			err = desc.Handler(ss, s)
		} else {
			info := &grpc.StreamServerInfo{
				FullMethod:     method,
				IsClientStream: desc.ClientStreams,
				IsServerStream: desc.ServerStreams,
			}
			err = srv.streamInt(ss, s, info, desc.Handler)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return s.closeWithSendError(err)
	}
}
