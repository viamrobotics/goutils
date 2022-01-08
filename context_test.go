package utils

import (
	"context"
	"testing"
	"time"

	"go.viam.com/test"
)

func TestMergeContext(t *testing.T) {
	ctx1 := context.Background()
	mergedCtx, mergedCtxCancel := MergeContext(ctx1, ctx1)
	select {
	case <-mergedCtx.Done():
	default:
	}
	mergedCtxCancel()
	<-mergedCtx.Done()
	test.That(t, mergedCtx.Err(), test.ShouldBeError, context.Canceled)

	ctx1, ctx1Cancel := context.WithCancel(context.Background())
	mergedCtx, mergedCtxCancel = MergeContext(ctx1, ctx1)
	select {
	case <-mergedCtx.Done():
	default:
	}
	ctx1Cancel()
	<-mergedCtx.Done()
	test.That(t, mergedCtx.Err(), test.ShouldBeError, context.Canceled)
	mergedCtxCancel()

	ctx1, ctx1Cancel = context.WithCancel(context.Background())
	mergedCtx, mergedCtxCancel = MergeContext(context.Background(), ctx1)
	select {
	case <-mergedCtx.Done():
	default:
	}
	ctx1Cancel()
	<-mergedCtx.Done()
	test.That(t, mergedCtx.Err(), test.ShouldBeError, context.Canceled)
	mergedCtxCancel()
}

func TestMergeContextWithTimeout(t *testing.T) {
	ctx1 := context.Background()
	mergedCtx, mergedCtxCancel := MergeContextWithTimeout(ctx1, ctx1, time.Second)
	select {
	case <-mergedCtx.Done():
	default:
	}
	<-mergedCtx.Done()
	test.That(t, mergedCtx.Err(), test.ShouldBeError, context.DeadlineExceeded)
	mergedCtxCancel()

	ctx1, ctx1Cancel := context.WithCancel(context.Background())
	mergedCtx, mergedCtxCancel = MergeContextWithTimeout(ctx1, ctx1, time.Hour)
	select {
	case <-mergedCtx.Done():
	default:
	}
	ctx1Cancel()
	<-mergedCtx.Done()
	test.That(t, mergedCtx.Err(), test.ShouldBeError, context.Canceled)
	mergedCtxCancel()

	ctx1, ctx1Cancel = context.WithCancel(context.Background())
	mergedCtx, mergedCtxCancel = MergeContextWithTimeout(context.Background(), ctx1, time.Hour)
	select {
	case <-mergedCtx.Done():
	default:
	}
	ctx1Cancel()
	<-mergedCtx.Done()
	test.That(t, mergedCtx.Err(), test.ShouldBeError, context.Canceled)
	mergedCtxCancel()
}
