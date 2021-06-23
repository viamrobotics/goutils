package rpcwebrtc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"go.viam.com/utils"
	mongoutils "go.viam.com/utils/mongo"
)

func init() {
	mongoutils.MustRegisterNamespace(&MongoDBCallQueueDBName, &MongoDBCallQueueCollName)
}

// A MongoDBCallQueue is an MongoDB implementation of a call queue designed to be used for
// multi-node, distributed deployments.
type MongoDBCallQueue struct {
	coll *mongo.Collection
}

// Database and collection names used by the MongoDBCallQueue.
var (
	MongoDBCallQueueDBName            = "rpc"
	MongoDBCallQueueCollName          = "calls"
	mongodbCallQueueExpireAfter int32 = 10
	mongoDBCallQueueIndexes           = []mongo.IndexModel{
		{
			Keys: bson.D{
				{"host", 1},
			},
		},
		{
			Keys: bson.D{
				{"started_at", 1},
			},
			Options: &options.IndexOptions{
				ExpireAfterSeconds: &mongodbCallQueueExpireAfter,
			},
		},
	}
)

// NewMongoDBCallQueue returns a new MongoDB based call queue where calls are transferred
// through the given client.
// TODO(https://github.com/viamrobotics/core/issues/108): more efficient, multiplexed change streams;
// uniquely identify host ephemerally
// TODO(https://github.com/viamrobotics/core/issues/109): max queue size
func NewMongoDBCallQueue(client *mongo.Client) (*MongoDBCallQueue, error) {
	coll := client.Database(MongoDBCallQueueDBName).Collection(MongoDBCallQueueCollName)
	if err := mongoutils.EnsureIndexes(coll, mongoDBCallQueueIndexes...); err != nil {
		return nil, err
	}
	return &MongoDBCallQueue{coll: coll}, nil
}

type mongodbCall struct {
	ID          primitive.ObjectID `bson:"_id"`
	Host        string             `bson:"host"`
	StartedAt   time.Time          `bson:"started_at"`
	CallerSDP   string             `bson:"caller_sdp"`
	Answered    bool               `bson:"answered"`
	AnswererSDP string             `bson:"answerer_sdp,omitempty"`
	Error       string             `bson:"error,omitempty"`
}

const (
	callIDField          = "_id"
	callHostField        = "host"
	callAnsweredField    = "answered"
	callAnswererSDPField = "answerer_sdp"
	callErrorField       = "error"
)

// SendOffer sends an offer associated with the given SDP to the given host.
func (queue *MongoDBCallQueue) SendOffer(ctx context.Context, host, sdp string) (string, error) {
	call := mongodbCall{
		ID:        primitive.NewObjectID(),
		Host:      host,
		CallerSDP: sdp,
	}

	cs, err := queue.coll.Watch(ctx, []bson.D{
		{
			{"$match", bson.D{
				{"operationType", mongoutils.ChangeEventOperationTypeUpdate},
				{fmt.Sprintf("documentKey.%s", callIDField), call.ID},
			}},
		},
	}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return "", err
	}
	defer func() {
		utils.UncheckedError(cs.Close(ctx))
	}()

	// need to watch before insertion to avoid a race
	csNext, csNextCancel := mongoutils.ChangeStreamNextBackground(ctx, cs)
	defer csNextCancel()
	defer func() { <-csNext }()

	call.StartedAt = time.Now()
	if _, err := queue.coll.InsertOne(ctx, call); err != nil {
		return "", err
	}

	next := <-csNext
	if next.Error != nil {
		return "", next.Error
	}

	var callResp mongodbCall
	if err := next.Event.FullDocument.Unmarshal(&callResp); err != nil {
		return "", err
	}

	if callResp.Error != "" {
		return "", errors.New(callResp.Error)
	}
	return callResp.AnswererSDP, nil
}

// RecvOffer receives the next offer for the given host. It should respond with an answer
// once a decision is made.
func (queue *MongoDBCallQueue) RecvOffer(ctx context.Context, host string) (CallOfferResponder, error) {
	// Start watching
	cs, err := queue.coll.Watch(ctx, []bson.D{
		{
			{"$match", bson.D{
				{"operationType", mongoutils.ChangeEventOperationTypeInsert},
				{fmt.Sprintf("fullDocument.%s", callHostField), host},
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		utils.UncheckedError(cs.Close(ctx))
	}()

	csNext, csNextCancel := mongoutils.ChangeStreamNextBackground(ctx, cs)
	defer csNextCancel()
	defer func() { <-csNext }()

	// but also check first if there is anything for us.
	// It is okay if we find something that someone else is working on answering
	// since we will eventually fail to connect with one peer. We also expect
	// only one host to try receiving at a time anyway.
	result := queue.coll.FindOne(ctx, bson.D{
		{callHostField, host},
		{callAnsweredField, false},
	})
	var callReq mongodbCall
	err = result.Decode(&callReq)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}

	if err == nil {
		return &mongoDBCallOfferResponder{callReq, queue.coll}, nil
	}

	next := <-csNext
	if next.Error != nil {
		return nil, next.Error
	}

	if err := next.Event.FullDocument.Unmarshal(&callReq); err != nil {
		return nil, err
	}

	return &mongoDBCallOfferResponder{callReq, queue.coll}, nil
}

type mongoDBCallOfferResponder struct {
	call mongodbCall
	coll *mongo.Collection
}

func (resp *mongoDBCallOfferResponder) SDP() string {
	return resp.call.CallerSDP
}

func (resp *mongoDBCallOfferResponder) Respond(ctx context.Context, ans CallAnswer) error {

	toSet := bson.D{{callAnsweredField, true}}
	if ans.Err == nil {
		toSet = append(toSet, bson.E{callAnswererSDPField, ans.SDP})
	} else {
		toSet = append(toSet, bson.E{callErrorField, ans.Err.Error()})
	}

	// we do not care if we did not match/update anything
	_, err := resp.coll.UpdateOne(
		ctx,
		bson.D{
			{callIDField, resp.call.ID},
		},
		bson.D{
			{"$set", toSet},
		},
	)
	return err
}
