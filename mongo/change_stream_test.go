package mongoutils_test

import (
	"context"
	"testing"

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
	result := mongoutils.ChangeStreamBackground(cancelCtx, cs)
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
	result = mongoutils.ChangeStreamBackground(cancelCtx, cs)
	defer func() {
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

func TestChangeStreamNextBackground(t *testing.T) {
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
	result := mongoutils.ChangeStreamNextBackground(cancelCtx, cs)
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
	result = mongoutils.ChangeStreamNextBackground(cancelCtx, cs)
	defer func() {
		for range result {
		}
	}()
	defer ctxCancel()
	doc := bson.D{{"_id", primitive.NewObjectID()}}
	errCh := make(chan error, 1)
	go func() {
		_, err := coll.InsertOne(context.Background(), doc)
		errCh <- err
	}()
	next = <-result
	test.That(t, next.Error, test.ShouldBeNil)
	test.That(t, <-errCh, test.ShouldBeNil)
	var retDoc bson.D
	test.That(t, next.Event.FullDocument.Unmarshal(&retDoc), test.ShouldBeNil)
	test.That(t, retDoc, test.ShouldResemble, doc)
}
