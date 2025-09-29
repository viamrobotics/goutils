package rpc

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	mongoutils "go.viam.com/utils/mongo"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

type WebRTCRateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

func init() {
	mongoutils.MustRegisterNamespace(&MongodbWebRTCCallQueueDBName, &mongodbmongodbRateLimiterCollName)
}

var (
	rateLimitDenials = statz.NewCounter1[string]("signaling/rate_limits_denials", statz.MetricConfig{
		Description: "Total number of requests rate limited.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "id", Description: "Fusion Auth ID of the client being rate limited."},
		},
	})
)

// Database configuration
var (
	mongodbmongodbRateLimiterCollName = "rate_limiter"
	mongodbWebRCRateLimiterTTLName    = "rate_limit_expire"
)

// A rateLimitDocument represents a request record in MongoDB
type rateLimitDocument struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Key       string             `bson:"key"`
	CreatedAt time.Time          `bson:"created_at"`
	ExpiresAt time.Time          `bson:"expires_at"`
}

// RateLimitConfig
type RateLimitConfig struct {
	MaxRequests int
	Window      time.Duration
}

// Default rate limit configuration
var defaultRateLimitConfig = RateLimitConfig{
	MaxRequests: 25,
	Window:      time.Minute,
}

// mongodbRateLimiter
type mongodbRateLimiter struct {
	client        *mongo.Client
	rateLimitColl *mongo.Collection
	config        RateLimitConfig
	logger        utils.ZapCompatibleLogger
}

// NewMongoDBRateLimiter
func NewMongoDBRateLimiter(
	client *mongo.Client,
	logger utils.ZapCompatibleLogger,
) (*mongodbRateLimiter, error) {
	rateLimitColl := client.Database(MongodbWebRTCCallQueueDBName).Collection(mongodbmongodbRateLimiterCollName)

	maxTTL := int32(2 * time.Minute.Seconds())
	indexes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "expires_at", Value: 1}},
			Options: &options.IndexOptions{
				Name:               &mongodbWebRCRateLimiterTTLName,
				ExpireAfterSeconds: &maxTTL,
			},
		},
		{
			Keys: bson.D{
				{Key: "fusion_auth_id", Value: 1},
				{Key: "created_at", Value: -1},
			},
		},
	}

	if err := mongoutils.EnsureIndexes(context.Background(), rateLimitColl, indexes...); err != nil {
		return nil, fmt.Errorf("failed to create rate limiter indexes: %w", err)
	}

	return &mongodbRateLimiter{
		client:        client,
		rateLimitColl: rateLimitColl,
		config:        defaultRateLimitConfig,
		logger:        logger,
	}, nil
}

// Allow
func (rl *mongodbRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if err := rl.recordRequest(ctx, key); err != nil {
		rl.logger.Errorw("failed to record rate limit request", "error", err)
		return true, err
	}

	if err := rl.checkRateLimit(ctx, key); err != nil {
		rateLimitDenials.Inc(key)
		return true, err
	}

	return true, nil
}

// checkRateLimit
func (rl *mongodbRateLimiter) checkRateLimit(ctx context.Context, key string) error {
	now := time.Now()
	windowStart := now.Add(-rl.config.Window)

	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	filter := bson.M{
		"key":       key,
		"timestamp": bson.M{"$gte": windowStart},
	}

	count, err := rl.rateLimitColl.CountDocuments(dbCtx, filter)
	if err != nil {
		rl.logger.Errorw("rate limit count query failed", "error", err, "key", key)
		return err
	}

	limit := int64(rl.config.MaxRequests)
	if count >= limit {
		return status.Errorf(codes.ResourceExhausted,
			"rate limit exceeded: %d requests in %v (limit: %d) for %s",
			count, rl.config.Window, limit, key)
	}

	return nil
}

// recordRequest
func (rl *mongodbRateLimiter) recordRequest(ctx context.Context, key string) error {
	now := time.Now()

	doc := rateLimitDocument{
		Key:       key,
		CreatedAt: now,
		ExpiresAt: now.Add(2 * time.Minute),
	}

	dbCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	_, err := rl.rateLimitColl.InsertOne(dbCtx, doc)
	return err
}

// memoryRateLimiter is a no-op rate limiter for testing and internal signaling use.
type memoryRateLimiter struct{}

func NewMemoryRateLimiter() *memoryRateLimiter {
	return &memoryRateLimiter{}
}

func (rl *memoryRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return true, nil
}
