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

func init() {
	mongoutils.MustRegisterNamespace(&mongodbWebRTCCallQueueDBName, &mongodbWebRTCRateLimiterCollName)
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
	mongodbWebRTCRateLimiterCollName = "rate_limiter"
	mongodbWebRTCRateLimiterTTLName  = "rate_limit_expire"
)

// RateLimitConfig specifies the configuration for rate limiting in terms of maximum requests allowed in a given time window.
type RateLimitConfig struct {
	MaxRequests int
	Window      time.Duration
}

// A MongoDBRateLimiter is a MongoDB implementation of a continuous sliding rate limiter designed to be used for
// multi-node, distributed deployments.
type MongoDBRateLimiter struct {
	rateLimitColl *mongo.Collection
	config        RateLimitConfig
	logger        utils.ZapCompatibleLogger
}

// NewMongoDBRateLimiter returns a new MongoDB based rate limiter where requests are allowed or denied based on how many
// requests have been made by a specific key (e.g., method + auth ID) within a certain time window specified by the limit
// provided by the config.
func NewMongoDBRateLimiter(
	ctx context.Context,
	client *mongo.Client,
	config RateLimitConfig,
	logger utils.ZapCompatibleLogger,
) (*MongoDBRateLimiter, error) {
	rateLimitColl := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCRateLimiterCollName)

	ttlSeconds := int32(0)
	indexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "expires_at", Value: 1}},
			Options: &options.IndexOptions{
				Name:               &mongodbWebRTCRateLimiterTTLName,
				ExpireAfterSeconds: &ttlSeconds,
			},
		},
	}

	if err := mongoutils.EnsureIndexes(ctx, rateLimitColl, indexes...); err != nil {
		return nil, err
	}

	return &MongoDBRateLimiter{
		rateLimitColl: rateLimitColl,
		config:        config,
		logger:        logger,
	}, nil
}

// Allow checks if a request is within rate limits and records it atomically.
// The filter only matches if the count of requests within the window are below the limit.
// The update creates a new array that adds the current timestamp and removes old timestamps outside the window.
// This prevents race conditions and keeps the requests array bounded.
func (rl *MongoDBRateLimiter) Allow(ctx context.Context, key string) error {
	// Ensure a document for the key exists or create one to handle first request case since a $expr filter
	// can't check for non-existence and create the document if it doesn't exist
	_, err := rl.rateLimitColl.UpdateOne(ctx,
		bson.M{"_id": key},
		bson.M{"$setOnInsert": bson.M{
			"requests": bson.A{},
			"expires_at": bson.M{
				"$dateAdd": bson.M{
					"startDate": "$$NOW",
					"unit":      "second",
					"amount":    rl.config.Window.Seconds(),
				},
			},
		}},
		options.Update().SetUpsert(true))
	if err != nil {
		rl.logger.Errorw("rate limit doc existence check failed", "error", err, "key", key)
		return err
	}

	// Filter: only match if request count within the most recent window for this key is < MaxRequests
	filter := bson.M{
		"_id": key,
		"$expr": bson.M{
			"$lt": bson.A{
				bson.M{
					"$size": bson.M{
						"$filter": bson.M{
							"input": "$requests",
							"as":    "req",
							"cond": bson.M{
								"$gte": bson.A{
									"$$req",
									bson.M{
										"$dateSubtract": bson.M{
											"startDate": "$$NOW",
											"unit":      "second",
											"amount":    rl.config.Window.Seconds(),
										},
									},
								},
							},
						},
					},
				},
				rl.config.MaxRequests,
			},
		},
	}

	// Update: create new requests array with current timestamp and old timestamps outside the window removed
	update := bson.A{
		bson.M{
			"$set": bson.M{
				"requests": bson.M{
					"$concatArrays": bson.A{
						bson.M{
							"$filter": bson.M{
								"input": "$requests",
								"as":    "req",
								"cond": bson.M{
									"$gte": bson.A{
										"$$req",
										bson.M{
											"$dateSubtract": bson.M{
												"startDate": "$$NOW",
												"unit":      "second",
												"amount":    rl.config.Window.Seconds(),
											},
										},
									},
								},
							},
						},
						bson.A{"$$NOW"},
					},
				},
				"expires_at": bson.M{
					"$dateAdd": bson.M{
						"startDate": "$$NOW",
						"unit":      "second",
						"amount":    rl.config.Window.Seconds(),
					},
				},
			},
		},
	}

	result, err := rl.rateLimitColl.UpdateOne(ctx, filter, update)
	if err != nil {
		rl.logger.Errorw("rate limit operation failed", "error", err, "key", key)
		return err
	}

	// No match means rate limit exceeded
	if result.MatchedCount == 0 {
		rateLimitDenials.Inc(key)
		return status.Errorf(codes.ResourceExhausted,
			"request exceeds rate limit (limit: %d in %v) for %s",
			rl.config.MaxRequests, rl.config.Window, key)
	}

	return nil
}
