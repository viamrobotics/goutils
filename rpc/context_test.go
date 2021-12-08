package rpc

import (
	"context"
	"testing"

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
