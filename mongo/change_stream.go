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

// ChangeEvent operation types.
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

// ChangeEventTo is used when operationType is rename; This document displays the
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
	Event       *ChangeEvent
	Error       error
	ResumeToken bson.Raw
}

// ChangeStreamBackground calls Next in a background goroutine that returns a series of events
// that can be received after the call is done. It will run until the given context is done.
// Additionally, on the return of this call, the resume token of the first getMore is returned.
// This resume token and subsequent ones returned in the channel can be used to follow events after
// the stream has started and after a particular event so as to not miss any events in between. For example,
// if the change stream were used to find an insertion of a document and find all updates after that insertion,
// you'd utilize the resume token from the channel. Without doing this you can either a) miss events or b)
// if no more events ever occurred, you may wait forever. Another example is starting a change stream to watch
// events for a document found/inserted out-of-band of the change stream. In this case you would use the
// resume token in the return value of this function.
func ChangeStreamBackground(ctx context.Context, cs *mongo.ChangeStream) (<-chan ChangeEventResult, bson.Raw) {
	// having this be buffered probably does not matter very much but it allows for the background
	// goroutine to be slightly ahead of the consumer in some cases.
	results := make(chan ChangeEventResult, 1)
	csStarted := make(chan bson.Raw, 1)
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
		for {
			if ctx.Err() != nil {
				return
			}

			var ce ChangeEvent
			if cs.TryNext(ctx) {
				if !csStartedOnce {
					csStartedOnce = true
					csStarted <- cs.ResumeToken()
				}
				rt := cs.ResumeToken()
				if err := cs.Decode(&ce); err != nil {
					sendResult(ChangeEventResult{Error: err, ResumeToken: rt})
					return
				}
				sendResult(ChangeEventResult{Event: &ce, ResumeToken: rt})
				continue
			}
			if !csStartedOnce {
				csStartedOnce = true
				csStarted <- cs.ResumeToken()
			}
			if cs.Next(ctx) {
				rt := cs.ResumeToken()
				if err := cs.Decode(&ce); err != nil {
					sendResult(ChangeEventResult{Error: err, ResumeToken: rt})
					return
				}
				sendResult(ChangeEventResult{Event: &ce, ResumeToken: rt})
				continue
			}
			sendResult(ChangeEventResult{Error: cs.Err(), ResumeToken: cs.ResumeToken()})
			return
		}
	})
	rt := <-csStarted
	return results, rt
}
