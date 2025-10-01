package rpc

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	mongoutils "go.viam.com/utils/mongo"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

// A WebRTCRateLimiter rate limits requests used in WebRTC signaling to control the rate of requests
// made based on a key, typically a combination of method and auth ID.
type WebRTCRateLimiter interface {
	Allow(ctx context.Context, key string) error
}

func init() {
	mongoutils.MustRegisterNamespace(&mongodbWebRTCCallQueueDBName, &mongodbmongodbRateLimiterCollName)
}

var rateLimitDenials = statz.NewCounter1[string]("signaling/rate_limits_denials", statz.MetricConfig{
	Description: "Total number of requests rate limited.",
	Unit:        units.Dimensionless,
	Labels: []statz.Label{
		{Name: "key", Description: "Method and auth ID of the client being rate limited."},
	},
})

// Database configuration and collection names for MongoDB rate limiter.
var (
	mongodbmongodbRateLimiterCollName = "rate_limiter"
	mongodbWebRCRateLimiterTTLName    = "rate_limit_expire"
)

type rateLimitDocument struct {
	ID        string      `bson:"_id"`
	Requests  []time.Time `bson:"requests"`
	ExpiresAt time.Time   `bson:"expires_at"`
}

// RateLimitConfig specifies the configuration for rate limiting in terms of maximum requests allowed in a given time window.
type RateLimitConfig struct {
	MaxRequests int
	Window      time.Duration
}

// A mongodbRateLimiter is a MongoDB implementation of a continuous sliding rate limiter designed to be used for
// multi-node, distributed deployments.
type mongodbRateLimiter struct {
	rateLimitColl *mongo.Collection
	config        RateLimitConfig
	logger        utils.ZapCompatibleLogger
}

// NewMongoDBRateLimiter returns a new MongoDB based rate limiter where requests are allowed or denied based on how many
// requests have been made by a specific key (e.g., method + auth ID) within a certain time window specified by the limit
// provided by the config.
func NewMongoDBRateLimiter(
	client *mongo.Client,
	logger utils.ZapCompatibleLogger,
	config RateLimitConfig,
) (WebRTCRateLimiter, error) {
	rateLimitColl := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbmongodbRateLimiterCollName)

	maxTTL := int32(2 * config.Window.Seconds())
	indexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "expires_at", Value: 1}},
			Options: &options.IndexOptions{
				Name:               &mongodbWebRCRateLimiterTTLName,
				ExpireAfterSeconds: &maxTTL,
			},
		},
	}

	if err := mongoutils.EnsureIndexes(context.Background(), rateLimitColl, indexes...); err != nil {
		return nil, err
	}

	return &mongodbRateLimiter{
		rateLimitColl: rateLimitColl,
		config:        config,
		logger:        logger,
	}, nil
}

// Allow inserts a timestamp for a request associated with the given key into MongoDB and determines if it is
// allowed based on the number of requests made in the last time window specificed by the rate limiting configuration.
// The document for each key contains an array of timestamps representing the times of requests made, which is trimmed
// to twice the maximum allowed requests to prevent unbounded growth, and only expires after no requests have been made for
// twice the time window duration.
func (rl *mongodbRateLimiter) Allow(ctx context.Context, key string) error {
	now := time.Now()
	windowStart := now.Add(-rl.config.Window)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	filter := bson.M{"_id": key}
	update := bson.M{
		"$push": bson.M{
			"requests": bson.M{
				"$each":  []time.Time{now},
				"$slice": -(rl.config.MaxRequests * 2),
			},
		},
		"$set": bson.M{"expires_at": now.Add(2 * time.Minute)},
	}

	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)

	var doc rateLimitDocument
	err := rl.rateLimitColl.FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc)
	if err != nil {
		rl.logger.Errorw("rate limit operation failed", "error", err, "key", key)
		return err
	}

	count := 0
	for _, reqTime := range doc.Requests {
		if reqTime.After(windowStart) {
			count++
		}
	}

	if count > rl.config.MaxRequests {
		rateLimitDenials.Inc(key)
		return status.Errorf(codes.ResourceExhausted,
			"request exceeds rate limit (limit: %d in %v) for %s",
			rl.config.MaxRequests, rl.config.Window, key)
	}

	return nil
}
