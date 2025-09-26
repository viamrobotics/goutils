package rpc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	mongoutils "go.viam.com/utils/mongo"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

func init() {
	mongoutils.MustRegisterNamespace(&MongodbWebRTCCallQueueDBName, &mongodbWebRTCRateLimiterCollName)
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
	mongodbWebRTCRateLimiterCollName = "rate_limiter"
	mongodbWebRCRateLimiterTTLName   = "rate_limit_expire"
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

// SignalingRateLimiter
type SignalingRateLimiter struct {
	client        *mongo.Client
	rateLimitColl *mongo.Collection
	config        RateLimitConfig
	logger        utils.ZapCompatibleLogger
}

// NewSignalingRateLimiter
func NewSignalingRateLimiter(
	client *mongo.Client,
	logger utils.ZapCompatibleLogger,
) (*SignalingRateLimiter, error) {
	rateLimitColl := client.Database(MongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCRateLimiterCollName)

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

	return &SignalingRateLimiter{
		client:        client,
		rateLimitColl: rateLimitColl,
		config:        defaultRateLimitConfig,
		logger:        logger,
	}, nil
}

// Allow
func (rl *SignalingRateLimiter) Allow(ctx context.Context) error {
	clientInfo, err := rl.extractClientInfo(ctx)
	if err != nil {
		rl.logger.Errorw("failed to extract client info for rate limiting", "error", err)
		return err
	}

	if err := rl.checkRateLimit(ctx, clientInfo.Key); err != nil {
		rateLimitDenials.Inc(clientInfo.Key)
		return err
	}

	if err := rl.recordRequest(ctx, clientInfo); err != nil {
		rl.logger.Errorw("failed to record rate limit request", "error", err)
	}

	return nil
}

// ClientInfo
type ClientInfo struct {
	Key string
}

// extractClientInfo
func (rl *SignalingRateLimiter) extractClientInfo(ctx context.Context) (*ClientInfo, error) {
	info := &ClientInfo{}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if authHeaders := md.Get("authorization"); len(authHeaders) > 0 {
			authHeader := authHeaders[0]
			if strings.HasPrefix(authHeader, "Bearer ") {
				info.Key = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				return nil, status.Errorf(codes.Unauthenticated, "invalid authorization header format")
			}
		}
	}

	return info, nil
}

// checkRateLimit
func (rl *SignalingRateLimiter) checkRateLimit(ctx context.Context, fusionAuthID string) error {
	now := time.Now()
	windowStart := now.Add(-rl.config.Window)

	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	filter := bson.M{
		"fusion_auth_id": fusionAuthID,
		"timestamp":      bson.M{"$gte": windowStart},
	}

	count, err := rl.rateLimitColl.CountDocuments(dbCtx, filter)
	if err != nil {
		rl.logger.Errorw("rate limit count query failed", "error", err, "fusion_auth_id", fusionAuthID)
		return err
	}

	limit := int64(rl.config.MaxRequests)
	if count >= limit {
		return status.Errorf(codes.ResourceExhausted,
			"rate limit exceeded: %d requests in %v (limit: %d) for %s",
			count, rl.config.Window, limit, fusionAuthID)
	}

	return nil
}

// recordRequest
func (rl *SignalingRateLimiter) recordRequest(ctx context.Context, clientInfo *ClientInfo) error {
	now := time.Now()

	doc := rateLimitDocument{
		Key:       clientInfo.Key,
		CreatedAt: now,
		ExpiresAt: now.Add(2 * time.Minute),
	}

	dbCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	_, err := rl.rateLimitColl.InsertOne(dbCtx, doc)
	return err
}
