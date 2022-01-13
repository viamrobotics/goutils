package rpc

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
	mongoutils.MustRegisterNamespace(&mongodbWebRTCCallQueueDBName, &mongodbWebRTCCallQueueCollName)
}

// A mongoDBWebRTCCallQueue is an MongoDB implementation of a call queue designed to be used for
// multi-node, distributed deployments.
type mongoDBWebRTCCallQueue struct {
	activeBackgroundWorkers sync.WaitGroup
	coll                    *mongo.Collection

	cancelCtx  context.Context
	cancelFunc func()
}

// Database and collection names used by the mongoDBWebRTCCallQueue.
var (
	mongodbWebRTCCallQueueDBName            = "rpc"
	mongodbWebRTCCallQueueCollName          = "calls"
	mongodbWebRTCCallQueueExpireAfter int32 = int32(getDefaultOfferDeadline().Seconds())
	mongodbWebRTCCallQueueIndexes           = []mongo.IndexModel{
		{
			Keys: bson.D{
				{webrtcCallHostField, 1},
			},
		},
		{
			Keys: bson.D{
				{webrtcCallStartedAtField, 1},
			},
			Options: &options.IndexOptions{
				ExpireAfterSeconds: &mongodbWebRTCCallQueueExpireAfter,
			},
		},
	}
)

// NewMongoDBWebRTCCallQueue returns a new MongoDB based call queue where calls are transferred
// through the given client.
// TODO(https://github.com/viamrobotics/goutils/issues/20): more efficient, multiplexed change streams;
// uniquely identify host ephemerally
// TODO(https://github.com/viamrobotics/goutils/issues/21): max queue size.
func NewMongoDBWebRTCCallQueue(client *mongo.Client) (WebRTCCallQueue, error) {
	coll := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCCallQueueCollName)
	if err := mongoutils.EnsureIndexes(coll, mongodbWebRTCCallQueueIndexes...); err != nil {
		return nil, err
	}
	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	return &mongoDBWebRTCCallQueue{
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

type mongodbWebRTCCall struct {
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
	webrtcCallIDField                 = "_id"
	webrtcCallHostField               = "host"
	webrtcCallStartedAtField          = "started_at"
	webrtcCallCallerCandidatesField   = "caller_candidates"
	webrtcCallCallerDoneField         = "caller_done"
	webrtcCallCallerErrorField        = "caller_error"
	webrtcCallAnsweredField           = "answered"
	webrtcCallAnswererSDPField        = "answerer_sdp"
	webrtcCallAnswererCandidatesField = "answerer_candidates"
	webrtcCallAnswererDoneField       = "answerer_done"
	webrtcCallAnswererErrorField      = "answerer_error"
)

// SendOfferInit initializes an offer associated with the given SDP to the given host.
// It returns a UUID to track/authenticate the offer over time, the initial SDP for the
// sender to start its peer connection with, as well as a channel to receive candidates on
// over time.
func (queue *mongoDBWebRTCCallQueue) SendOfferInit(
	ctx context.Context,
	host, sdp string,
	disableTrickle bool,
) (string, <-chan WebRTCCallAnswer, <-chan struct{}, func(), error) {
	newUUID := uuid.NewString()
	call := mongodbWebRTCCall{
		ID:        newUUID,
		Host:      host,
		CallerSDP: sdp,
	}

	cs, err := queue.coll.Watch(ctx, []bson.D{
		{
			{"$match", bson.D{
				{"operationType", mongoutils.ChangeEventOperationTypeUpdate},
				{fmt.Sprintf("documentKey.%s", webrtcCallIDField), call.ID},
			}},
		},
	}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return "", nil, nil, nil, err
	}

	// need to watch before insertion to avoid a race
	sendCtx, sendCtxCancel := utils.MergeContext(ctx, queue.cancelCtx)
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

	answererResponses := make(chan WebRTCCallAnswer, 1)
	sendAnswer := func(answer WebRTCCallAnswer) bool {
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
				sendAnswer(WebRTCCallAnswer{Err: next.Error})
				return
			}

			var callResp mongodbWebRTCCall
			if err := next.Event.FullDocument.Unmarshal(&callResp); err != nil {
				sendAnswer(WebRTCCallAnswer{Err: err})
				return
			}

			if callResp.AnswererError != "" {
				sendAnswer(WebRTCCallAnswer{Err: errors.New(callResp.AnswererError)})
				return
			}

			if !haveInitSDP && callResp.AnswererSDP != "" {
				haveInitSDP = true
				if !sendAnswer(WebRTCCallAnswer{InitialSDP: &callResp.AnswererSDP}) {
					return
				}
			}

			if len(callResp.AnswererCandidates) > candLen {
				prevCandLen := candLen
				newCandLen := len(callResp.AnswererCandidates) - candLen
				candLen += newCandLen
				for i := 0; i < newCandLen; i++ {
					cand := iceCandidateFromMongo(callResp.AnswererCandidates[prevCandLen+i])
					if !sendAnswer(WebRTCCallAnswer{Candidate: &cand}) {
						return
					}
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
func (queue *mongoDBWebRTCCallQueue) SendOfferUpdate(ctx context.Context, host, uuid string, candidate webrtc.ICECandidateInit) error {
	updateResult, err := queue.coll.UpdateOne(ctx, bson.D{
		{webrtcCallIDField, uuid},
		{webrtcCallHostField, host},
	}, bson.D{{"$push", bson.D{{webrtcCallCallerCandidatesField, iceCandidateToMongo(&candidate)}}}})
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
func (queue *mongoDBWebRTCCallQueue) SendOfferDone(ctx context.Context, host, uuid string) error {
	updateResult, err := queue.coll.UpdateOne(ctx, bson.D{
		{webrtcCallIDField, uuid},
		{webrtcCallHostField, host},
	}, bson.D{{"$set", bson.D{{webrtcCallCallerDoneField, true}}}})
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
func (queue *mongoDBWebRTCCallQueue) SendOfferError(ctx context.Context, host, uuid string, err error) error {
	updateResult, err := queue.coll.UpdateOne(ctx, bson.D{
		{webrtcCallIDField, uuid},
		{webrtcCallHostField, host},
	}, bson.D{{"$set", bson.D{{webrtcCallCallerErrorField, err.Error()}}}})
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
func (queue *mongoDBWebRTCCallQueue) RecvOffer(ctx context.Context, hosts []string) (WebRTCCallOfferExchange, error) {
	// Start watching for an offer inserted
	cs, err := queue.coll.Watch(ctx, []bson.D{
		{
			{"$match", bson.D{
				{"operationType", mongoutils.ChangeEventOperationTypeInsert},
				{fmt.Sprintf("fullDocument.%s", webrtcCallHostField), bson.D{{"$in", hosts}}},
			}},
		},
	}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return nil, err
	}

	recvOfferCtx, recvOfferCtxCancel := utils.MergeContext(ctx, queue.cancelCtx)
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
		{webrtcCallHostField, bson.D{{"$in", hosts}}},
		{webrtcCallAnsweredField, false},
		{webrtcCallStartedAtField, bson.D{{"$gt", time.Now().Add(-getDefaultOfferDeadline())}}},
	})
	var callReq mongodbWebRTCCall
	err = result.Decode(&callReq)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}

	getFirstResult := func() error {
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case next, ok := <-csOfferNext:
			if !ok {
				return errors.New("no next result")
			}
			if next.Error != nil {
				return next.Error
			}

			return next.Event.FullDocument.Unmarshal(&callReq)
		}
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
				{fmt.Sprintf("documentKey.%s", webrtcCallIDField), callReq.ID},
			}},
		},
	}, options.ChangeStream().
		SetFullDocument(options.UpdateLookup).
		SetResumeAfter(rt))
	if err != nil {
		return nil, err
	}

	recvCtx, recvCtxCancel := utils.MergeContextWithTimeout(ctx, queue.cancelCtx, getDefaultOfferDeadline())
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
	exchange := mongoDBWebRTCCallOfferExchange{
		call:             callReq,
		coll:             queue.coll,
		callerCandidates: make(chan webrtc.ICECandidateInit),
		callerDoneCtx:    callerDoneCtx,
	}
	setErr := func(errToSet error) {
		_, err := queue.coll.UpdateOne(
			ctx,
			bson.D{
				{webrtcCallIDField, callReq.ID},
			},
			bson.D{{"$set", bson.D{{webrtcCallAnsweredField, errToSet}}}},
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

			var callUpdate mongodbWebRTCCall
			if err := next.Event.FullDocument.Unmarshal(&callUpdate); err != nil {
				setErr(err)
				return
			}

			if callUpdate.CallerError != "" {
				exchange.callerErr = errors.New(callUpdate.CallerError)
				return
			}

			if len(callUpdate.CallerCandidates) > candLen {
				prevCandLen := candLen
				newCandLen := len(callUpdate.CallerCandidates) - candLen
				candLen += newCandLen
				for i := 0; i < newCandLen; i++ {
					cand := iceCandidateFromMongo(callUpdate.CallerCandidates[prevCandLen+i])
					if !sendCandidate(cand) {
						return
					}
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
func (queue *mongoDBWebRTCCallQueue) Close() error {
	queue.cancelFunc()
	queue.activeBackgroundWorkers.Wait()
	return nil
}

type mongoDBWebRTCCallOfferExchange struct {
	call             mongodbWebRTCCall
	coll             *mongo.Collection
	callerCandidates chan webrtc.ICECandidateInit
	callerDoneCtx    context.Context
	callerErr        error
}

func (resp *mongoDBWebRTCCallOfferExchange) UUID() string {
	return resp.call.ID
}

func (resp *mongoDBWebRTCCallOfferExchange) SDP() string {
	return resp.call.CallerSDP
}

func (resp *mongoDBWebRTCCallOfferExchange) DisableTrickleICE() bool {
	return resp.call.DisableTrickle
}

func (resp *mongoDBWebRTCCallOfferExchange) CallerCandidates() <-chan webrtc.ICECandidateInit {
	return resp.callerCandidates
}

func (resp *mongoDBWebRTCCallOfferExchange) CallerDone() <-chan struct{} {
	return resp.callerDoneCtx.Done()
}

func (resp *mongoDBWebRTCCallOfferExchange) CallerErr() error {
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

func (resp *mongoDBWebRTCCallOfferExchange) AnswererRespond(ctx context.Context, ans WebRTCCallAnswer) error {
	toSet := bson.D{{webrtcCallAnsweredField, true}}
	var toPush bson.D
	switch {
	case ans.InitialSDP != nil:
		toSet = append(toSet, bson.E{webrtcCallAnswererSDPField, ans.InitialSDP})
	case ans.Candidate != nil:
		toPush = append(toPush, bson.E{webrtcCallAnswererCandidatesField, iceCandidateToMongo(ans.Candidate)})
	case ans.Err != nil:
		toSet = append(toSet, bson.E{webrtcCallAnswererErrorField, ans.Err.Error()})
	default:
		return errors.New("expected either SDP, ICE candidate, or error to be set")
	}

	update := bson.D{{"$set", toSet}}
	if len(toPush) > 0 {
		update = append(update, bson.E{"$push", toPush})
	}

	updateResult, err := resp.coll.UpdateOne(
		ctx,
		bson.D{
			{webrtcCallIDField, resp.call.ID},
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

func (resp *mongoDBWebRTCCallOfferExchange) AnswererDone(ctx context.Context) error {
	updateResult, err := resp.coll.UpdateOne(ctx, bson.D{
		{webrtcCallIDField, resp.UUID()},
		{webrtcCallHostField, resp.call.Host},
	}, bson.D{{"$set", bson.D{{webrtcCallAnswererDoneField, true}}}})
	if err != nil {
		return err
	}
	if updateResult.MatchedCount == 0 || updateResult.ModifiedCount == 0 {
		return newInactiveOfferErr(resp.call.ID)
	}
	return nil
}
