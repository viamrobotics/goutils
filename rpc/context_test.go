package rpc

import (
	"context"
	"testing"

	"go.viam.com/test"
)

func TestContextHost(t *testing.T) {
	ctx := context.Background()
	someHost := "myhost"
	ctx = ContextWithHost(ctx, someHost)
	someHost2 := ContextHost(context.Background())
	test.That(t, someHost2, test.ShouldEqual, "")
	someHost2 = ContextHost(ctx)
	test.That(t, someHost2, test.ShouldEqual, someHost)
}
