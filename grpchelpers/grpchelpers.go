// Package grpchelpers provides some grpc helper utilities.
package grpchelpers

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

// ConnectivityState allows callers to check the connectivity state of
// the connection and to be notified of state transition events
// see https://github.com/grpc/grpc-go/blob/v1.79.3/clientconn.go#L724
type ConnectivityState interface {
	GetState() connectivity.State
	// WaitForStateChange blocks until the connectivity state of the connection changes from
	// sourceState or ctx expires, returning true in the former case and false in the latter.
	WaitForStateChange(ctx context.Context, sourceState connectivity.State) (bool, error)
}

// ConnConnectivityState returns the connectivity.State of the current connection, if available.
// When offline, conn's state should alternate between connectivity.TransientFailure and connectivity.Connecting while the grpc library
// tries to reconnect in the background at a cadence defined by the backoff specified with grpc.ConnectParams (default: exponential & capped
// at 2 mins). Making an RPC call will not bypass this (it will not trigger an immediate reconnection attempt).
// Note: using this to test the connection before making an RPC call, while often useful, is considered an antipattern.
// Official docs recommend using the grpc.WaitForReady CallOption instead if possible.
func ConnConnectivityState(conn grpc.ClientConnInterface) (connectivity.State, bool) {
	if cs, ok := conn.(ConnectivityState); ok {
		return cs.GetState(), true
	}
	return -1, false
}

// ErrConnNotReady can be used to signal that the conn may not be in a immediately usable state (user defined).
var ErrConnNotReady = errors.New("conn is not in a ready state")
