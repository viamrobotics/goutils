package mongoutils_test

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.viam.com/test"

	mongoutils "go.viam.com/utils/mongo"
	"go.viam.com/utils/testutils"
)

func TestChangeStreamBackground(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)
	dbName, collName := testutils.NewMongoDBNamespace()
	coll := client.Database(dbName).Collection(collName)

	cs, err := coll.Watch(context.Background(), []bson.D{
		{
			{"$match", bson.D{}},
		},
	}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	test.That(t, err, test.ShouldBeNil)

	cancelCtx, ctxCancel := context.WithCancel(context.Background())
	result, _, _ := mongoutils.ChangeStreamBackground(cancelCtx, cs, nil)
	ctxCancel()
	next := <-result
	test.That(t, next.Error, test.ShouldWrap, context.Canceled)

	cs, err = coll.Watch(context.Background(), []bson.D{
		{
			{"$match", bson.D{}},
		},
	}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	test.That(t, err, test.ShouldBeNil)

	cancelCtx, ctxCancel = context.WithCancel(context.Background())
	result, _, _ = mongoutils.ChangeStreamBackground(cancelCtx, cs, nil)
	defer func() {
		//nolint:revive
		for range result {
		}
	}()
	defer ctxCancel()
	times := 3
	docs := make([]bson.D, 0, times)
	for i := 0; i < times; i++ {
		docs = append(docs, bson.D{{"_id", primitive.NewObjectID()}})
	}
	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < times; i++ {
			_, err := coll.InsertOne(context.Background(), docs[i])
			errCh <- err
		}
	}()
	for i := 0; i < times; i++ {
		next = <-result
		test.That(t, next.Error, test.ShouldBeNil)
		test.That(t, <-errCh, test.ShouldBeNil)
		var retDoc bson.D
		test.That(t, next.Event.FullDocument.Unmarshal(&retDoc), test.ShouldBeNil)
		test.That(t, retDoc, test.ShouldResemble, docs[i])
	}
}

func TestChangeStreamBackgroundResumeTokenAdvancement(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)
	dbName, collName := testutils.NewMongoDBNamespace()
	coll := client.Database(dbName).Collection(collName)

	cs, err := coll.Watch(context.Background(), []bson.D{
		{
			{"$match", bson.D{}},
		},
	},
		options.ChangeStream().SetFullDocument(options.UpdateLookup),
		options.ChangeStream().SetMaxAwaitTime(time.Second),
	)
	test.That(t, err, test.ShouldBeNil)

	cancelCtx, ctxCancel := context.WithCancel(context.Background())
	result, currentToken, currentClusterTime := mongoutils.ChangeStreamBackground(cancelCtx, cs, nil)
	defer func() {
		//nolint:revive
		for range result {
		}
	}()
	defer ctxCancel()
	test.That(t, currentToken, test.ShouldNotBeNil)
	test.That(t, currentClusterTime, test.ShouldBeZeroValue)
	times := 3
	docs := make([]bson.D, 0, times)
	for i := 0; i < times; i++ {
		docs = append(docs, bson.D{{"_id", primitive.NewObjectID()}})
	}
	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < times; i++ {
			_, err := coll.InsertOne(context.Background(), docs[i])
			errCh <- err
		}
	}()
	for i := 0; i < times; i++ {
		next := <-result
		test.That(t, next.Error, test.ShouldBeNil)
		test.That(t, <-errCh, test.ShouldBeNil)
		test.That(t, next.ResumeToken, test.ShouldNotBeNil)
		test.That(t, currentToken, test.ShouldNotResemble, next.ResumeToken)
		test.That(t, next.Event.ClusterTime, test.ShouldNotBeZeroValue)
		test.That(t, currentClusterTime, test.ShouldNotResemble, next.Event.ClusterTime)
		var retDoc bson.D
		test.That(t, next.Event.FullDocument.Unmarshal(&retDoc), test.ShouldBeNil)
		test.That(t, retDoc, test.ShouldResemble, docs[i])
	}
}

func TestChangeStreamBackgroundInvalidate(t *testing.T) {
	client := testutils.BackingMongoDBClient(t)
	dbName, collName := testutils.NewMongoDBNamespace()
	coll := client.Database(dbName).Collection(collName)

	_, err := coll.InsertOne(context.Background(), bson.D{})
	test.That(t, err, test.ShouldBeNil)

	cs, err := coll.Watch(context.Background(), []bson.D{
		{
			{"$match", bson.D{
				{"operationType", bson.D{{"$in", []interface{}{
					mongoutils.ChangeEventOperationTypeInsert,
					mongoutils.ChangeEventOperationTypeUpdate,
					mongoutils.ChangeEventOperationTypeDelete,
					mongoutils.ChangeEventOperationTypeInvalidate,
				}}}},
			}},
		},
	},
		options.ChangeStream().SetFullDocument(options.UpdateLookup),
		options.ChangeStream().SetMaxAwaitTime(time.Second),
	)
	test.That(t, err, test.ShouldBeNil)

	cancelCtx, ctxCancel := context.WithCancel(context.Background())
	result, _, _ := mongoutils.ChangeStreamBackground(cancelCtx, cs, nil)
	defer func() {
		//nolint:revive
		for range result {
		}
	}()
	defer ctxCancel()

	go func() {
		coll.Drop(context.Background())
	}()

	event := <-result
	test.That(t, event.Error, test.ShouldResemble, mongoutils.ErrChangeStreamInvalidateEvent)

	//nolint:revive
	for range result {
	}

	someID := primitive.NewObjectID()
	_, err = coll.InsertOne(context.Background(), bson.D{{"_id", someID}})
	test.That(t, err, test.ShouldBeNil)

	cs, err = coll.Watch(context.Background(), []bson.D{
		{
			{"$match", bson.D{
				{"operationType", bson.D{{"$in", []interface{}{
					mongoutils.ChangeEventOperationTypeInsert,
					mongoutils.ChangeEventOperationTypeUpdate,
					mongoutils.ChangeEventOperationTypeDelete,
					mongoutils.ChangeEventOperationTypeInvalidate,
				}}}},
			}},
		},
	},
		options.ChangeStream().SetFullDocument(options.UpdateLookup),
		options.ChangeStream().SetMaxAwaitTime(time.Second),
		options.ChangeStream().SetStartAfter(event.ResumeToken),
	)
	test.That(t, err, test.ShouldBeNil)

	result, _, _ = mongoutils.ChangeStreamBackground(cancelCtx, cs, nil)
	event = <-result
	test.That(t, event.Error, test.ShouldBeNil)
	test.That(t, event.Event.DocumentKey, test.ShouldResemble, bson.D{{"_id", someID}})
}
