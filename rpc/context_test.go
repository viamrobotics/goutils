package rpc

import (
	"context"
	"testing"

	"github.com/pion/webrtc/v4"
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
	ctx = contextWithPeerConnection(ctx, &pc)
	_, ok := ContextPeerConnection(context.Background())
	test.That(t, ok, test.ShouldBeFalse)
	pc2, ok := ContextPeerConnection(ctx)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, pc2, test.ShouldEqual, &pc)
}
