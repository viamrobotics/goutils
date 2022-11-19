// Package server implement an echo server.
package server

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"github.com/pkg/errors"

	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
)

// Server implements a simple echo service.
type Server struct {
	mu sync.Mutex
	echopb.UnimplementedEchoServiceServer
	fail       bool
	authorized bool

	// prevents a package cycle. DO NOT set this to anything other
	// than the real thing.
	ContextAuthEntity   func(ctx context.Context) interface{}
	ContextAuthClaims   func(ctx context.Context) interface{}
	ContextAuthUniqueID func(ctx context.Context) string

	expectedAuthEntity   string
	ExpectedAuthUniqueID string
}

// SetFail instructs the server to fail at certain points in its execution.
func (srv *Server) SetFail(fail bool) {
	srv.mu.Lock()
	srv.fail = fail
	srv.mu.Unlock()
}

// SetAuthorized instructs the server to check authorization at certain points.
func (srv *Server) SetAuthorized(authorized bool) {
	srv.mu.Lock()
	srv.authorized = authorized
	srv.mu.Unlock()
}

// SetExpectedAuthEntity sets the expected auth entity
func (srv *Server) SetExpectedAuthEntity(authEntity string) {
	srv.mu.Lock()
	srv.expectedAuthEntity = authEntity
	srv.mu.Unlock()
}

// Echo responds back with the same message.
func (srv *Server) Echo(ctx context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.fail {
		return nil, errors.New("whoops")
	}
	if srv.authorized {
		expectedAuthEntity := srv.expectedAuthEntity
		if expectedAuthEntity == "" {
			expectedAuthEntity = "somespecialinterface"
		}
		if srv.ContextAuthEntity(ctx) != expectedAuthEntity {
			fmt.Println("hmm", srv.ContextAuthEntity(ctx))
			return nil, errors.New("unauthenticated or unauthorized")
		}
		if srv.ContextAuthClaims(ctx) != nil {
			return nil, errors.New("did not expect auth claims here")
		}
		uniqueID := srv.ContextAuthUniqueID(ctx)
		if srv.ExpectedAuthUniqueID == "" {
			if _, err := uuid.Parse(uniqueID); err != nil {
				return nil, err
			}
		} else if uniqueID != srv.ExpectedAuthUniqueID {
			return nil, errors.Errorf("expected auth unique id %q; got %q", srv.ExpectedAuthUniqueID, uniqueID)
		}
	}
	return &echopb.EchoResponse{Message: req.Message}, nil
}

// EchoMultiple responds back with the same message one character at a time.
func (srv *Server) EchoMultiple(req *echopb.EchoMultipleRequest, server echopb.EchoService_EchoMultipleServer) error {
	cnt := len(req.Message)
	for i := 0; i < cnt; i++ {
		select {
		case <-server.Context().Done():
			return server.Context().Err()
		default:
		}
		if err := server.Send(&echopb.EchoMultipleResponse{Message: req.Message[i : i+1]}); err != nil {
			return err
		}
	}
	return nil
}

// EchoBiDi responds back with the same message one character at a time for each message sent to it.
func (srv *Server) EchoBiDi(server echopb.EchoService_EchoBiDiServer) error {
	for {
		select {
		case <-server.Context().Done():
			return server.Context().Err()
		default:
		}
		req, err := server.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		cnt := len(req.Message)
		for i := 0; i < cnt; i++ {
			select {
			case <-server.Context().Done():
				return server.Context().Err()
			default:
			}
			if err := server.Send(&echopb.EchoBiDiResponse{Message: req.Message[i : i+1]}); err != nil {
				return err
			}
		}
	}
}
