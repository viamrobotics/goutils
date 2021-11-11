package mongoutils

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"go.viam.com/utils"
)

// A ChangeEvent represents all possible fields that a change stream response document can have.
type ChangeEvent struct {
	ID                       bson.RawValue                `bson:"_id"`
	OperationType            ChangeEventOperationType     `bson:"operationType"`
	FullDocument             bson.RawValue                `bson:"fullDocument"`
	NS                       ChangeEventNamespace         `bson:"ns"`
	To                       ChangeEventTo                `bson:"to"`
	DocumentKey              bson.D                       `bson:"documentKey"`
	UpdateDescription        ChangeEventUpdateDescription `bson:"UpdateDescription"`
	ClusterTime              primitive.Timestamp          `bson:"clusterTime"`
	TransactionNumber        uint64                       `bson:"txnNumber"`
	LogicalSessionIdentifier bson.D                       `bson:"lsid"`
}

// ChangeEventOperationType is the type of operation that occurred.
type ChangeEventOperationType string

// ChangeEvent operation types
const (
	ChangeEventOperationTypeInsert       = ChangeEventOperationType("insert")
	ChangeEventOperationTypeDelete       = ChangeEventOperationType("delete")
	ChangeEventOperationTypeReplace      = ChangeEventOperationType("replace")
	ChangeEventOperationTypeUpdate       = ChangeEventOperationType("update")
	ChangeEventOperationTypeDrop         = ChangeEventOperationType("drop")
	ChangeEventOperationTypeRename       = ChangeEventOperationType("rename")
	ChangeEventOperationTypeDropDatabase = ChangeEventOperationType("dropDatabase")
	ChangeEventOperationTypeInvalidate   = ChangeEventOperationType("invalidate")
)

// ChangeEventNamespace is the namespace (database and or collection) affected by the event.
type ChangeEventNamespace struct {
	Database   string `bson:"db"`
	Collection string `bson:"coll"`
}

// ChangeEventTo is used when when operationType is rename; This document displays the
// new name for the ns collection. This document is omitted for all other values of operationType.
type ChangeEventTo ChangeEventNamespace

// ChangeEventUpdateDescription is a document describing the fields that were updated or removed
// by the update operation.
// This document and its fields only appears if the operationType is update.
type ChangeEventUpdateDescription struct {
	UpdatedFields bson.D   `bson:"updatedFields"`
	RemovedFields []string `bson:"removedFields"`
}

// ChangeEventResult represents either an event happening or an error that happened
// along the way.
type ChangeEventResult struct {
	Event *ChangeEvent
	Error error
}

// changeStreamBackground calls Next in the background and returns once at least one attempt has
// been made. It returns a result that can be received after the call is done.
func changeStreamBackground(ctx context.Context, cs *mongo.ChangeStream, once bool) <-chan ChangeEventResult {
	results := make(chan ChangeEventResult, 1)
	csStarted := make(chan struct{}, 1)
	sendResult := func(result ChangeEventResult) {
		select {
		case <-ctx.Done():
			// try once more
			select {
			case results <- result:
			default:
			}
		case results <- result:
		}
	}
	utils.PanicCapturingGo(func() {
		defer close(results)

		csStartedOnce := false
		atLeastOnce := false
		for !(once && atLeastOnce) {
			if ctx.Err() != nil {
				return
			}

			var ce ChangeEvent
			if cs.TryNext(ctx) {
				if !csStartedOnce {
					csStartedOnce = true
					close(csStarted)
				}
				if err := cs.Decode(&ce); err != nil {
					sendResult(ChangeEventResult{Error: err})
					return
				}
				sendResult(ChangeEventResult{Event: &ce})
				atLeastOnce = true
				continue
			}
			if !csStartedOnce {
				csStartedOnce = true
				close(csStarted)
			}
			if cs.Next(ctx) {
				if err := cs.Decode(&ce); err != nil {
					sendResult(ChangeEventResult{Error: err})
					return
				}
				sendResult(ChangeEventResult{Event: &ce})
				atLeastOnce = true
				continue
			}
			sendResult(ChangeEventResult{Error: cs.Err()})
			return
		}
	})
	<-csStarted
	return results
}

// ChangeStreamBackground calls Next in the background and returns once at least one attempt has
// been made. It returns a series of result that can be received after the call is done. It will run
// until the given context is done.
func ChangeStreamBackground(ctx context.Context, cs *mongo.ChangeStream) <-chan ChangeEventResult {
	return changeStreamBackground(ctx, cs, false)
}

// ChangeStreamNextBackground calls Next in the background and returns once at least one attempt has
// been made. It returns a result that can be received after the call is done. It will run
// until the given context is done.
func ChangeStreamNextBackground(ctx context.Context, cs *mongo.ChangeStream) <-chan ChangeEventResult {
	return changeStreamBackground(ctx, cs, true)
}
