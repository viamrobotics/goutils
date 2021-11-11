package rpcwebrtc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
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
	activeBackgroundWorkers sync.WaitGroup
	coll                    *mongo.Collection

	cancelCtx  context.Context
	cancelFunc func()
}

// Database and collection names used by the MongoDBCallQueue.
var (
	MongoDBCallQueueDBName            = "rpc"
	MongoDBCallQueueCollName          = "calls"
	mongodbCallQueueExpireAfter int32 = int32(getDefaultOfferDeadline().Seconds())
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
	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	return &MongoDBCallQueue{
		coll:       coll,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}, nil
}

type mongodbICECandidate struct {
	Candidate        string  `bson:"candidate"`
	SDPMid           *string `bson:"sdp_mid"`
	SDPMLineIndex    *uint16 `bson:"sdp_m_line_index"`
	UsernameFragment *string `bson:"username_fragment"`
}

type mongodbCall struct {
	ID                 string                `bson:"_id"`
	Host               string                `bson:"host"`
	StartedAt          time.Time             `bson:"started_at"`
	CallerSDP          string                `bson:"caller_sdp"`
	CallerCandidates   []mongodbICECandidate `bson:"caller_candidates,omitempty"`
	CallerDone         bool                  `bson:"caller_done"`
	CallerError        string                `bson:"caller_error,omitempty"`
	DisableTrickle     bool                  `bson:"disable_trickle"`
	Answered           bool                  `bson:"answered"`
	AnswererSDP        string                `bson:"answerer_sdp,omitempty"`
	AnswererCandidates []mongodbICECandidate `bson:"answerer_candidates,omitempty"`
	AnswererDone       bool                  `bson:"answerer_done"`
	AnswererError      string                `bson:"answerer_error,omitempty"`
}

const (
	callIDField                 = "_id"
	callHostField               = "host"
	callCallerCandidatesField   = "caller_candidates"
	callCallerDoneField         = "caller_done"
	callCallerErrorField        = "caller_error"
	callAnsweredField           = "answered"
	callAnswererSDPField        = "answerer_sdp"
	callAnswererCandidatesField = "answerer_candidates"
	callAnswererDoneField       = "answerer_done"
	callAnswererErrorField      = "answerer_error"
)

// SendOfferInit initializes an offer associated with the given SDP to the given host.
// It returns a UUID to track/authenticate the offer over time, the initial SDP for the
// sender to start its peer connection with, as well as a channel to receive candidates on
// over time.
func (queue *MongoDBCallQueue) SendOfferInit(ctx context.Context, host, sdp string, disableTrickle bool) (string, <-chan CallAnswer, <-chan struct{}, func(), error) {
	newUUID := uuid.NewString()
	call := mongodbCall{
		ID:        newUUID,
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
		return "", nil, nil, nil, err
	}

	// need to watch before insertion to avoid a race
	sendCtx, sendCtxCancel := context.WithTimeout(queue.cancelCtx, getDefaultOfferDeadline())
	csNext := mongoutils.ChangeStreamBackground(sendCtx, cs)

	var ctxDeadlineExceedViaCS bool
	cleanup := func() {
		defer func() {
			for range csNext {
			}
		}()
		if !ctxDeadlineExceedViaCS {
			defer sendCtxCancel()
		}
	}
	var successful bool
	defer func() {
		if successful {
			return
		}
		cleanup()
	}()

	call.StartedAt = time.Now()
	if _, err := queue.coll.InsertOne(ctx, call); err != nil {
		return "", nil, nil, nil, err
	}

	answererResponses := make(chan CallAnswer, 1)
	sendAnswer := func(answer CallAnswer) bool {
		if answer.Err != nil && errors.Is(answer.Err, context.DeadlineExceeded) {
			ctxDeadlineExceedViaCS = true
		}
		select {
		case <-sendCtx.Done():
			// try once more
			select {
			case answererResponses <- answer:
			default:
			}
			return false
		case answererResponses <- answer:
			return true
		}
	}
	queue.activeBackgroundWorkers.Add(1)
	utils.PanicCapturingGo(func() {
		defer queue.activeBackgroundWorkers.Done()
		defer func() {
			cleanup()
		}()
		defer close(answererResponses)

		haveInitSDP := false
		candLen := len(call.AnswererCandidates)
		for {
			next, ok := <-csNext
			if !ok {
				return
			}

			if next.Error != nil {
				sendAnswer(CallAnswer{Err: next.Error})
				return
			}

			var callResp mongodbCall
			if err := next.Event.FullDocument.Unmarshal(&callResp); err != nil {
				sendAnswer(CallAnswer{Err: err})
				return
			}

			if callResp.AnswererError != "" {
				sendAnswer(CallAnswer{Err: errors.New(callResp.AnswererError)})
				return
			}

			if len(callResp.AnswererCandidates) > candLen {
				candLen++
				cand := iceCandidateFromMongo(callResp.AnswererCandidates[len(callResp.AnswererCandidates)-1])
				if !sendAnswer(CallAnswer{Candidate: &cand}) {
					return
				}
			}

			if !haveInitSDP && callResp.AnswererSDP != "" {
				haveInitSDP = true
				if !sendAnswer(CallAnswer{InitialSDP: &callResp.AnswererSDP}) {
					return
				}
			}

			if callResp.AnswererDone {
				return
			}
		}
	})
	successful = true
	return newUUID, answererResponses, sendCtx.Done(), sendCtxCancel, nil
}

// SendOfferUpdate updates the offer associated with the given UUID with a newly discovered
// ICE candidate.
func (queue *MongoDBCallQueue) SendOfferUpdate(ctx context.Context, host, uuid string, candidate webrtc.ICECandidateInit) error {
	updateResult, err := queue.coll.UpdateOne(ctx, bson.D{
		{callIDField, uuid},
		{callHostField, host},
	}, bson.D{{"$push", bson.D{{callCallerCandidatesField, iceCandidateToMongo(&candidate)}}}})
	if err != nil {
		return err
	}
	if updateResult.MatchedCount == 0 {
		return newInactiveOfferErr(uuid)
	}
	return nil
}

// SendOfferDone informs the queue that the offer associated with the UUID is done sending any
// more information.
func (queue *MongoDBCallQueue) SendOfferDone(ctx context.Context, host, uuid string) error {
	updateResult, err := queue.coll.UpdateOne(ctx, bson.D{
		{callIDField, uuid},
		{callHostField, host},
	}, bson.D{{"$set", bson.D{{callCallerDoneField, true}}}})
	if err != nil {
		return err
	}
	if updateResult.MatchedCount == 0 {
		return newInactiveOfferErr(uuid)
	}
	return nil
}

// SendOfferError informs the queue that the offer associated with the UUID has encountered
// an error from the sender side.
func (queue *MongoDBCallQueue) SendOfferError(ctx context.Context, host, uuid string, err error) error {
	updateResult, err := queue.coll.UpdateOne(ctx, bson.D{
		{callIDField, uuid},
		{callHostField, host},
	}, bson.D{{"$set", bson.D{{callCallerErrorField, err.Error()}}}})
	if err != nil {
		return err
	}
	if updateResult.MatchedCount == 0 {
		return newInactiveOfferErr(uuid)
	}
	return nil
}

// RecvOffer receives the next offer for the given host. It should respond with an answer
// once a decision is made.
func (queue *MongoDBCallQueue) RecvOffer(ctx context.Context, host string) (CallOfferExchange, error) {
	// Start watching for an offer inserted
	cs, err := queue.coll.Watch(ctx, []bson.D{
		{
			{"$match", bson.D{
				{"operationType", mongoutils.ChangeEventOperationTypeInsert},
				{fmt.Sprintf("fullDocument.%s", callHostField), host},
			}},
		},
	}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return nil, err
	}

	recvOfferCtx, recvOfferCtxCancel := context.WithTimeout(queue.cancelCtx, getDefaultOfferDeadline())
	csOfferNext := mongoutils.ChangeStreamBackground(recvOfferCtx, cs)

	cleanup := func() {
		defer func() {
			for range csOfferNext {
			}
		}()
		defer recvOfferCtxCancel()
	}
	var recvOfferSuccessful bool
	defer func() {
		if recvOfferSuccessful {
			return
		}
		cleanup()
	}()

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

	getFirstResult := func() error {
		if err == nil {
			return nil
		}

		next, ok := <-csOfferNext
		if !ok {
			return errors.New("no next result")
		}
		if next.Error != nil {
			return next.Error
		}

		return next.Event.FullDocument.Unmarshal(&callReq)
	}
	if err := getFirstResult(); err != nil {
		return nil, err
	}
	recvOfferSuccessful = true
	cleanup()

	rt := cs.ResumeToken()
	cs, err = queue.coll.Watch(ctx, []bson.D{
		{
			{"$match", bson.D{
				{"operationType", mongoutils.ChangeEventOperationTypeUpdate},
				{fmt.Sprintf("documentKey.%s", callIDField), callReq.ID},
			}},
		},
	}, options.ChangeStream().
		SetFullDocument(options.UpdateLookup).
		SetResumeAfter(rt))
	if err != nil {
		return nil, err
	}
	recvCtx, recvCtxCancel := context.WithTimeout(queue.cancelCtx, getDefaultOfferDeadline())
	csNext := mongoutils.ChangeStreamBackground(recvCtx, cs)

	cleanup = func() {
		defer func() {
			for range csNext {
			}
		}()
		defer recvCtxCancel()
	}
	var successful bool
	defer func() {
		if successful {
			return
		}
		cleanup()
	}()

	callerDoneCtx, callerDoneCancel := context.WithCancel(context.Background())
	exchange := mongoDBCallOfferExchange{
		call:             callReq,
		coll:             queue.coll,
		callerCandidates: make(chan webrtc.ICECandidateInit),
		callerDoneCtx:    callerDoneCtx,
	}
	setErr := func(errToSet error) {
		_, err := queue.coll.UpdateOne(
			ctx,
			bson.D{
				{callIDField, callReq.ID},
			},
			bson.D{{"$set", bson.D{{callAnsweredField, errToSet}}}},
		)
		utils.UncheckedError(err)
	}
	sendCandidate := func(cand webrtc.ICECandidateInit) bool {
		select {
		case <-recvCtx.Done():
			// try once more
			select {
			case exchange.callerCandidates <- cand:
			default:
			}
			return false
		case exchange.callerCandidates <- cand:
			return true
		}
	}
	queue.activeBackgroundWorkers.Add(1)
	utils.PanicCapturingGo(func() {
		defer queue.activeBackgroundWorkers.Done()
		defer callerDoneCancel()
		defer cleanup()

		candLen := len(callReq.CallerCandidates)
		for {
			next, ok := <-csNext
			if !ok {
				return
			}
			if next.Error != nil {
				setErr(next.Error)
				return
			}

			var callUpdate mongodbCall
			if err := next.Event.FullDocument.Unmarshal(&callUpdate); err != nil {
				setErr(err)
				return
			}

			if callUpdate.CallerError != "" {
				exchange.callerErr = errors.New(callUpdate.CallerError)
				return
			}

			if len(callUpdate.CallerCandidates) > candLen {
				candLen++
				cand := callUpdate.CallerCandidates[len(callUpdate.CallerCandidates)-1]
				if !sendCandidate(iceCandidateFromMongo(cand)) {
					return
				}
			}

			if callUpdate.CallerDone {
				return
			}
		}
	})
	successful = true
	return &exchange, nil
}

func iceCandidateFromMongo(i mongodbICECandidate) webrtc.ICECandidateInit {
	candidate := webrtc.ICECandidateInit{
		Candidate: i.Candidate,
	}
	if i.SDPMid != nil {
		val := *i.SDPMid
		candidate.SDPMid = &val
	}
	if i.SDPMLineIndex != nil {
		val := *i.SDPMLineIndex
		candidate.SDPMLineIndex = &val
	}
	if i.UsernameFragment != nil {
		val := *i.UsernameFragment
		candidate.UsernameFragment = &val
	}
	return candidate
}

func iceCandidateToMongo(i *webrtc.ICECandidateInit) mongodbICECandidate {
	candidate := mongodbICECandidate{
		Candidate: i.Candidate,
	}
	if i.SDPMid != nil {
		val := *i.SDPMid
		candidate.SDPMid = &val
	}
	if i.SDPMLineIndex != nil {
		val := *i.SDPMLineIndex
		candidate.SDPMLineIndex = &val
	}
	if i.UsernameFragment != nil {
		val := *i.UsernameFragment
		candidate.UsernameFragment = &val
	}
	return candidate
}

// Close cancels all actives offers and waits to cleanly close all background workers.
func (queue *MongoDBCallQueue) Close() error {
	queue.cancelFunc()
	queue.activeBackgroundWorkers.Wait()
	return nil
}

type mongoDBCallOfferExchange struct {
	call             mongodbCall
	coll             *mongo.Collection
	callerCandidates chan webrtc.ICECandidateInit
	callerDoneCtx    context.Context
	callerErr        error
}

func (resp *mongoDBCallOfferExchange) UUID() string {
	return resp.call.ID
}

func (resp *mongoDBCallOfferExchange) SDP() string {
	return resp.call.CallerSDP
}

func (resp *mongoDBCallOfferExchange) DisableTrickleICE() bool {
	return resp.call.DisableTrickle
}

func (resp *mongoDBCallOfferExchange) CallerCandidates() <-chan webrtc.ICECandidateInit {
	return resp.callerCandidates
}

func (resp *mongoDBCallOfferExchange) CallerDone() <-chan struct{} {
	return resp.callerDoneCtx.Done()
}

func (resp *mongoDBCallOfferExchange) CallerErr() error {
	if resp.callerDoneCtx.Err() == nil {
		return nil
	}
	if resp.callerErr != nil {
		return resp.callerErr
	}
	if errors.Is(resp.callerDoneCtx.Err(), context.Canceled) {
		return nil
	}
	return resp.callerDoneCtx.Err()
}

func (resp *mongoDBCallOfferExchange) AnswererRespond(ctx context.Context, ans CallAnswer) error {
	toSet := bson.D{{callAnsweredField, true}}
	var toPush bson.D
	if ans.InitialSDP != nil {
		toSet = append(toSet, bson.E{callAnswererSDPField, ans.InitialSDP})
	} else if ans.Candidate != nil {
		toPush = append(toPush, bson.E{callAnswererCandidatesField, iceCandidateToMongo(ans.Candidate)})
	} else if ans.Err != nil {
		toSet = append(toSet, bson.E{callAnswererErrorField, ans.Err.Error()})
	} else {
		return errors.New("expected either SDP, ICE candidate, or error to be set")
	}

	update := bson.D{{"$set", toSet}}
	if len(toPush) > 0 {
		update = append(update, bson.E{"$push", toPush})
	}

	updateResult, err := resp.coll.UpdateOne(
		ctx,
		bson.D{
			{callIDField, resp.call.ID},
		},
		update,
	)
	if err != nil {
		return err
	}
	if updateResult.MatchedCount == 0 {
		return newInactiveOfferErr(resp.call.ID)
	}
	return nil
}

func (resp *mongoDBCallOfferExchange) AnswererDone(ctx context.Context) error {
	updateResult, err := resp.coll.UpdateOne(ctx, bson.D{
		{callIDField, resp.UUID()},
		{callHostField, resp.call.Host},
	}, bson.D{{"$set", bson.D{{callAnswererDoneField, true}}}})
	if err != nil {
		return err
	}
	if updateResult.MatchedCount == 0 || updateResult.ModifiedCount == 0 {
		return newInactiveOfferErr(resp.call.ID)
	}
	return nil
}
