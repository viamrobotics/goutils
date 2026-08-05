package rpc

import (
	"context"
	"testing"

	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
)

func TestContextHost(t *testing.T) {
	ctx := context.Background()
	someHost := "myhost"
	ctx = contextWithHost(ctx, someHost)
	someHost2 := contextHost(context.Background())
	test.That(t, someHost2, test.ShouldEqual, "")
	someHost2 = contextHost(ctx)
	test.That(t, someHost2, test.ShouldEqual, someHost)
}

func TestContextDialer(t *testing.T) {
	ctx := context.Background()
	cachedDialer := NewCachedDialer()
	ctx = ContextWithDialer(ctx, cachedDialer)
	cachedDialer2 := contextDialer(context.Background())
	test.That(t, cachedDialer2, test.ShouldBeNil)
	cachedDialer2 = contextDialer(ctx)
	test.That(t, cachedDialer2, test.ShouldEqual, cachedDialer)
}

func TestContextPeerConnection(t *testing.T) {
	ctx := context.Background()
	var pc webrtc.PeerConnection
	ctx = ContextWithPeerConnection(ctx, &pc)
	_, ok := ContextPeerConnection(context.Background())
	test.That(t, ok, test.ShouldBeFalse)
	pc2, ok := ContextPeerConnection(ctx)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, pc2, test.ShouldEqual, &pc)
}

func TestRequestTransportGRPCUnaryInterceptor(t *testing.T) {
	// No prior stamp (raw gRPC listener) → interceptor marks it native gRPC.
	var seen RequestTransport
	capture := func(ctx context.Context, req interface{}) (interface{}, error) {
		seen, _ = ContextRequestTransport(ctx)
		return struct{}{}, nil
	}
	_, _ = requestTransportGRPCUnaryInterceptor(context.Background(), nil, nil, capture)
	test.That(t, seen, test.ShouldEqual, RequestTransportGRPC)

	// A prior HTTP-layer stamp (e.g. gRPC-Web from a browser) is preserved.
	ctx := ContextWithRequestTransport(context.Background(), RequestTransportGRPCWeb)
	_, _ = requestTransportGRPCUnaryInterceptor(ctx, nil, nil, capture)
	test.That(t, seen, test.ShouldEqual, RequestTransportGRPCWeb)
}

func TestContextRequestTransport(t *testing.T) {
	_, ok := ContextRequestTransport(context.Background())
	test.That(t, ok, test.ShouldBeFalse)

	ctx := ContextWithRequestTransport(context.Background(), RequestTransportGRPC)
	rt, ok := ContextRequestTransport(ctx)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, rt, test.ShouldEqual, RequestTransportGRPC)

	ctx = ContextWithRequestTransport(context.Background(), RequestTransportGRPCWeb)
	rt, ok = ContextRequestTransport(ctx)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, rt, test.ShouldEqual, RequestTransportGRPCWeb)
}
