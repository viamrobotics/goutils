package rpc

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/testutils"
)

func TestMongoDBRateLimiter(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)
	logger := golog.NewTestLogger(t)

	config := RateLimitConfig{
		MaxRequests: 3,
		Window:      time.Second,
	}

	setUpLimiter := func(t *testing.T, ctx context.Context) *MongoDBRateLimiter {
		t.Helper()
		test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(ctx), test.ShouldBeNil)

		limiter, err := NewMongoDBRateLimiter(ctx, client, config, logger)
		test.That(t, err, test.ShouldBeNil)
		return limiter
	}

	t.Run("allows requests under limit", func(t *testing.T) {
		ctx := context.Background()
		key := "test"
		limiter := setUpLimiter(t, ctx)

		for i := 0; i < config.MaxRequests; i++ {
			err := limiter.Allow(ctx, key)
			test.That(t, err, test.ShouldBeNil)
		}
	})

	t.Run("denies requests over limit", func(t *testing.T) {
		ctx := context.Background()
		key := "test"
		limiter := setUpLimiter(t, ctx)

		// Fill up to the limit
		for i := 0; i < config.MaxRequests; i++ {
			err := limiter.Allow(ctx, key)
			test.That(t, err, test.ShouldBeNil)
		}

		err := limiter.Allow(ctx, key)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "request exceeds rate limit")
		errStatus := status.Convert(err)
		test.That(t, errStatus, test.ShouldNotBeNil)
		test.That(t, errStatus.Code(), test.ShouldEqual, codes.ResourceExhausted)
	})

	t.Run("sliding window resets after duration", func(t *testing.T) {
		ctx := context.Background()
		key := "test"
		limiter := setUpLimiter(t, ctx)

		// Fill up the limit
		for i := 0; i < config.MaxRequests; i++ {
			err := limiter.Allow(ctx, key)
			test.That(t, err, test.ShouldBeNil)
		}

		// Should be denied
		err := limiter.Allow(ctx, key)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "request exceeds rate limit")
		errStatus := status.Convert(err)
		test.That(t, errStatus, test.ShouldNotBeNil)
		test.That(t, errStatus.Code(), test.ShouldEqual, codes.ResourceExhausted)

		// Wait for window to pass and let requests expire
		time.Sleep(2*config.Window + 100*time.Millisecond)

		// Should be allowed again
		err = limiter.Allow(ctx, key)
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("different keys have separate limits", func(t *testing.T) {
		ctx := context.Background()
		key1 := "test1"
		key2 := "test2"
		limiter := setUpLimiter(t, ctx)

		// Fill key1's limit
		for i := 0; i < config.MaxRequests; i++ {
			err := limiter.Allow(ctx, key1)
			test.That(t, err, test.ShouldBeNil)
		}

		// Key1 should be denied
		err := limiter.Allow(ctx, key1)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "request exceeds rate limit")
		errStatus := status.Convert(err)
		test.That(t, errStatus, test.ShouldNotBeNil)
		test.That(t, errStatus.Code(), test.ShouldEqual, codes.ResourceExhausted)

		// Key2 should still be allowed
		err = limiter.Allow(ctx, key2)
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("trims old requests outside window", func(t *testing.T) {
		ctx := context.Background()
		key := "test"
		limiter := setUpLimiter(t, ctx)

		// Make initial requests (will be filtered out later)
		for i := 0; i < config.MaxRequests; i++ {
			err := limiter.Allow(ctx, key)
			test.That(t, err, test.ShouldBeNil)
		}

		// Wait for window to pass
		time.Sleep(2*config.Window + 100*time.Millisecond)

		// Make 1 new request
		err := limiter.Allow(ctx, key)
		test.That(t, err, test.ShouldBeNil)

		// Verify array contains exactly the 1 new request (old ones filtered out)
		var doc struct {
			Requests []primitive.DateTime `bson:"requests"`
		}
		err = limiter.rateLimitColl.FindOne(ctx, bson.M{"_id": key}).Decode(&doc)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, len(doc.Requests), test.ShouldEqual, 1)
	})

	t.Run("handles concurrent requests from same key", func(t *testing.T) {
		ctx := context.Background()
		key := "test"
		limiter := setUpLimiter(t, ctx)

		// Make double the number of requests concurrently
		numRequests := config.MaxRequests * 2
		errChan := make(chan error, numRequests)

		for i := 0; i < numRequests; i++ {
			go func() {
				err := limiter.Allow(ctx, key)
				errChan <- err
			}()
		}

		allowed := 0
		denied := 0
		for i := 0; i < numRequests; i++ {
			err := <-errChan
			if err == nil {
				allowed++
			} else {
				denied++
			}
		}

		// Should allow up to the limit and deny the rest
		test.That(t, allowed, test.ShouldEqual, config.MaxRequests)
		test.That(t, denied, test.ShouldEqual, config.MaxRequests)
	})

	t.Run("handles concurrent requests from different keys", func(t *testing.T) {
		ctx := context.Background()
		limiter := setUpLimiter(t, ctx)

		numUsers := 3
		allowedReqs := numUsers * config.MaxRequests
		totalReqs := allowedReqs + 1

		errChan := make(chan error, totalReqs)

		// Each user makes requests concurrently
		for i := 0; i < numUsers; i++ {
			for j := 0; j < config.MaxRequests; j++ {
				go func(userIndex int) {
					userKey := "call:user-" + strconv.Itoa(userIndex)
					err := limiter.Allow(ctx, userKey)
					errChan <- err
				}(i)
			}
		}

		// One more request from one of the users to test limit
		go func() {
			userKey := "call:user-0"
			err := limiter.Allow(ctx, userKey)
			errChan <- err
		}()

		// All requests should be allowed (each user under within limit) except the last one
		allowed := 0
		denied := 0
		for i := 0; i < totalReqs; i++ {
			err := <-errChan
			if err == nil {
				allowed++
			} else {
				denied++
			}
		}

		// Should have allowed all but one request
		test.That(t, allowed, test.ShouldEqual, allowedReqs)
		test.That(t, denied, test.ShouldEqual, 1)
	})
}
