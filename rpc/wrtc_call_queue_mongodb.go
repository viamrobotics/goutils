package rpc

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/viamrobotics/webrtc/v3"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.opencensus.io/trace"
	"go.uber.org/multierr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	mongoutils "go.viam.com/utils/mongo"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

func init() {
	mongoutils.MustRegisterNamespace(&mongodbWebRTCCallQueueDBName, &mongodbWebRTCCallQueueCallsCollName)
	mongoutils.MustRegisterNamespace(&mongodbWebRTCCallQueueDBName, &mongodbWebRTCCallQueueOperatorsCollName)
}

var (
	callChangeStreamFailures = statz.NewCounter1[string]("signaling/call_change_stream_failures", statz.MetricConfig{
		Description: "The number of times making a change stream fails.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "operator_id", Description: "The queue operator ID."},
		},
	})

	callAnswererTooBusy = statz.NewCounter2[string, string]("signaling/call_answerer_too_busy", statz.MetricConfig{
		Description: "The number of times all answerers were too busy to handle a new call.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "operator_id", Description: "The queue operator ID."},
			{Name: "hostname", Description: "The robot being requested"},
		},
	})

	operatorsCollUpdateFailures = statz.NewCounter2[string, string]("signaling/operators_coll_update_failures", statz.MetricConfig{
		Description: "The number of times the operator failed to update the operators collection.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "operator_id", Description: "The queue operator ID."},
			{Name: "reason", Description: "The reason for failure."},
		},
	})

	operatorsCollUpserts = statz.NewCounter1[string]("signaling/operators_coll_upserts", statz.MetricConfig{
		Description: "The number of times the operator upserted into the operators collection.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "operator_id", Description: "The queue operator ID."},
		},
	})

	exchangeChannelAtCapacity = statz.NewCounter1[string]("signaling/exchange_channel_at_capacity", statz.MetricConfig{
		Description: "The number of times a call exchange has it max channel capacity.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "operator_id", Description: "The queue operator ID."},
		},
	})

	activeHosts = statz.NewGauge1[string]("signaling/active_hosts", statz.MetricConfig{
		Description: "The number of hosts waiting for a call to come in or processing a call.",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "operator_id", Description: "The queue operator ID."},
		},
	})

	connectionEstablishmentAttempts = statz.NewCounter2[string, string](
		"signaling/connection_establishment_attempts",
		statz.MetricConfig{
			Description: "The total number of connection establishment attempts (offer initializations).",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
			},
		},
	)

	connectionEstablishmentFailures = statz.NewCounter2[string, string](
		"signaling/connection_establishment_failures",
		statz.MetricConfig{
			Description: "The total number of connection establishment failures (all caller or answerer errors).",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
			},
		},
	)

	connectionEstablishmentExpectedFailures = statz.NewCounter4[string, string, string, string](
		"signaling/connection_establishment_expected_failures",
		statz.MetricConfig{
			Description: "The total number of connection attempts that failed because they were blocked by the signaling server.",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
				{
					Name:        "reason",
					Description: "The reason the connection was blocked ('answerers_offline', 'too_many_callers', or 'other').",
				},
				{
					Name:        "online_recently",
					Description: "Whether the target machine was reportedly online in the last 10s (blocked potential success)",
				},
			},
		},
	)

	connectionEstablishmentCallerTimeouts = statz.NewCounter2[string, string](
		"signaling/connection_establishment_caller_timeouts",
		statz.MetricConfig{
			Description: "The total number of connection establishment failures that were timeouts on the caller side.",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
			},
		},
	)

	connectionEstablishmentCallerNonTimeoutErrors = statz.NewCounter2[string, string](
		"signaling/connection_establishment_caller_non_timeouts",
		statz.MetricConfig{
			Description: "The total number of connection establishment failures that were NOT timeouts on the caller side.",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
			},
		},
	)

	connectionEstablishmentAnswererTimeouts = statz.NewCounter2[string, string](
		"signaling/connection_establishment_answerer_timeouts",
		statz.MetricConfig{
			Description: "The total number of connection establishment failures that were timeouts on the answerer side.",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
			},
		},
	)

	connectionEstablishmentAnswererNonTimeoutErrors = statz.NewCounter2[string, string](
		"signaling/connection_establishment_answerer_non_timeouts",
		statz.MetricConfig{
			Description: "The total number of connection establishment failures that were NOT timeouts on the answerer side.",
			Unit:        units.Dimensionless,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
			},
		},
	)

	callExchangeDuration = statz.NewDistribution3[string, string, string](
		"signaling/call_exchange_duration",
		statz.MetricConfig{
			Description: "The duration of call exchanges from initialization to completion.",
			Unit:        units.Milliseconds,
			Labels: []statz.Label{
				{
					Name:        "sdk_type",
					Description: "The type of SDK attempting to connect.",
				},
				{
					Name:        "organization_id",
					Description: "The organization ID of the machine that is being connected to.",
				},
				{
					Name:        "result",
					Description: "The result of the call exchange (finished or failed).",
				},
			},
		},
		statz.ConnectionTimeDistribution,
	)
)

// A mongoDBWebRTCCallQueue is an MongoDB implementation of a call queue designed to be used for
// multi-node, distributed deployments.
type mongoDBWebRTCCallQueue struct {
	operatorID                         string
	hostCallerQueueSizeMatchAggStage   bson.D
	hostAnswererQueueSizeMatchAggStage bson.D
	hostAnswererOnlineMatchAggStage    bson.D
	activeBackgroundWorkers            sync.WaitGroup
	callsColl                          *mongo.Collection
	operatorsColl                      *mongo.Collection
	logger                             utils.ZapCompatibleLogger

	cancelCtx  context.Context
	cancelFunc func()

	csStateMu sync.RWMutex
	// this is a counter that increases based on errors / answerers or callers coming live
	// and indicates whether the changestream needs to swap
	csManagerSeq                atomic.Uint64
	csLastEventClusterTime      primitive.Timestamp
	csLastResumeToken           bson.Raw
	csTrackingHosts             utils.StringSet
	csAnswerersWaitingForNextCS []func()
	csStateUpdates              chan changeStreamStateUpdate
	csCtxCancel                 func()

	// function passed in during construction to update last_online timestamps for documents
	// in robot and robot_part based on this call-queue/operator.
	onAnswererLiveness func(hostnames []string, atTime time.Time)
	// function passed in during construction to check if the last_online timestamp in the
	// robot_part collection for hostname was in the past 10s (recently online).
	checkAnswererLiveness func(hostname string) bool

	// 1 caller/answerer -> 1 caller id -> 1 event stream
	callExchangeSubs map[string]map[*mongodbCallExchange]struct{}

	// M answerer -> N hosts -> 1 event stream
	waitingForNewCallSubs map[string]map[*mongodbNewCallEventHandler]struct{}
}

type changeStreamStateUpdate struct {
	ChangeStream <-chan mongoutils.ChangeEventResult
	ResumeToken  bson.Raw
	ClusterTime  primitive.Timestamp
}

// Database and collection names used by the mongoDBWebRTCCallQueue.
var (
	mongodbWebRTCCallQueueDBName             = "rpc"
	mongodbWebRTCCallQueueCallsCollName      = "calls"
	mongodbWebRTCCallQueueOperatorsCollName  = "operators"
	mongodbWebRTCCallQueueRPCCallExpireName  = "rpc_call_expire"
	mongodbWebRTCCallQueueOperatorExpireName = "operator_expire"
)

// this probably matches defaultMaxAnswerers on the signaling answerer.
const maxHostAnswerersSize = 2

// How long we want to delay clients before retrying to connect to an offline host.
const offlineHostRetryDelay = 5 * time.Second

const (
	exchangeFailed   = "exchange_failed"
	exchangeFinished = "exchange_finished"
)

// NewMongoDBWebRTCCallQueue returns a new MongoDB based call queue where calls are transferred
// through the given client. The operator ID must be unique (e.g. a hostname, container ID, UUID, etc.).
// Currently, the queue can grow to an unbounded size in terms of goroutines in memory but it is expected
// that this code is run in an auto scaling environment that bounds how many incoming requests there can
// be. The given max queue size specifies how many big a queue can be for a given host; the size is used
// as an approximation and at times may exceed the max as a performance/consistency balance of being
// a distributed queue.
func NewMongoDBWebRTCCallQueue(
	ctx context.Context,
	operatorID string,
	maxHostCallers uint64,
	client *mongo.Client,
	logger utils.ZapCompatibleLogger,
	onAnswererLiveness func(hostnames []string, atTime time.Time),
	checkAnswererLiveness func(hostname string) bool,
) (WebRTCCallQueue, error) {
	if operatorID == "" {
		return nil, errors.New("expected non-empty operatorID")
	}
	callsColl := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCCallQueueCallsCollName)
	operatorsColl := client.Database(mongodbWebRTCCallQueueDBName).Collection(mongodbWebRTCCallQueueOperatorsCollName)

	mongodbWebRTCCallQueueExpireAfter := int32(getDefaultOfferDeadline().Seconds())
	mongodbWebRTCCallQueueCallsIndexes := []mongo.IndexModel{
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
				Name:               &mongodbWebRTCCallQueueRPCCallExpireName,
				ExpireAfterSeconds: &mongodbWebRTCCallQueueExpireAfter,
			},
		},
	}

	expireAfterSecondsZero := int32(0)
	mongodbWebRTCCallQueueOperatorsIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				// queries on this wont be covered but we don't expect the fetch size to be very big.
				// the results of aggs around this field can also be cached if need be.
				{webrtcOperatorHostsHostCombinedField, 1},
			},
		},
		{
			Keys: bson.D{
				{webrtcOperatorExpireAtField, 1},
			},
			Options: &options.IndexOptions{
				Name:               &mongodbWebRTCCallQueueOperatorExpireName,
				ExpireAfterSeconds: &expireAfterSecondsZero,
			},
		},
	}

	if err := mongoutils.EnsureIndexes(ctx, callsColl, mongodbWebRTCCallQueueCallsIndexes...); err != nil {
		return nil, err
	}
	if err := mongoutils.EnsureIndexes(ctx, operatorsColl, mongodbWebRTCCallQueueOperatorsIndexes...); err != nil {
		return nil, err
	}

	result, err := operatorsColl.InsertOne(ctx, bson.D{
		{webrtcOperatorIDField, operatorID},
		{webrtcOperatorExpireAtField, time.Now().Add(operatorHeartbeatWindow)},
	})
	if err != nil {
		return nil, err
	}
	logger.Infow(
		"successfully added operator document to operators collection",
		"operator_id", operatorID, "document_id", result.InsertedID,
	)

	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	queue := &mongoDBWebRTCCallQueue{
		operatorID: operatorID,
		hostCallerQueueSizeMatchAggStage: bson.D{{"$match", bson.D{
			{"caller_size", bson.D{{"$gte", maxHostCallers}}},
		}}},
		// we use maxHostAnswerersSize * 2 to accommodate an answerer that
		// immedeiately reconnects
		hostAnswererQueueSizeMatchAggStage: bson.D{{"$match", bson.D{
			{"answerer_size", bson.D{{"$gte", maxHostAnswerersSize * 2}}},
		}}},
		hostAnswererOnlineMatchAggStage: bson.D{{"$match", bson.D{
			{"answerer_size", bson.D{{"$gt", 0}}},
		}}},

		callsColl:     callsColl,
		operatorsColl: operatorsColl,
		cancelCtx:     cancelCtx,
		cancelFunc:    cancelFunc,
		logger:        utils.AddFieldsToLogger(logger, "operator_id", operatorID),

		csStateUpdates:        make(chan changeStreamStateUpdate),
		callExchangeSubs:      map[string]map[*mongodbCallExchange]struct{}{},
		waitingForNewCallSubs: map[string]map[*mongodbNewCallEventHandler]struct{}{},
		onAnswererLiveness:    onAnswererLiveness,
		checkAnswererLiveness: checkAnswererLiveness,
	}

	queue.activeBackgroundWorkers.Add(2)
	utils.ManagedGo(queue.operatorLivenessLoop, queue.activeBackgroundWorkers.Done)
	utils.ManagedGo(queue.changeStreamManager, queue.activeBackgroundWorkers.Done)

	// wait for change stream to startup once before we start processing anything
	// since we need good track of resume tokens / cluster times initially
	// to keep an ordering.
	startOnce := make(chan struct{})
	var startOnceSync sync.Once

	queue.activeBackgroundWorkers.Add(1)
	utils.ManagedGo(func() {
		defer queue.csManagerSeq.Add(1) // helpful on panicked restart
		select {
		case <-queue.cancelCtx.Done():
			return
		case newState := <-queue.csStateUpdates:
			queue.processClusterEventState(newState.ResumeToken, newState.ClusterTime)
			startOnceSync.Do(func() {
				close(startOnce)
			})
			queue.subscriptionManager(newState.ChangeStream)
		}
	}, queue.activeBackgroundWorkers.Done)

	select {
	case <-queue.cancelCtx.Done():
		return nil, multierr.Combine(queue.Close(), queue.cancelCtx.Err())
	case <-startOnce:
	}

	return queue, nil
}

type mongodbICECandidate struct {
	Candidate        string  `bson:"candidate"`
	SDPMid           *string `bson:"sdp_mid"`
	SDPMLineIndex    *uint16 `bson:"sdp_m_line_index"`
	UsernameFragment *string `bson:"username_fragment"`
}

type mongodbWebRTCCall struct {
	ID                 string                `bson:"_id"`
	CallerOperatorID   string                `bson:"caller_operator_id"`
	AnswererOperatorID string                `bson:"answerer_operator_id"`
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
	SDKType            string                `bson:"sdk_type,omitempty"`
	OrganizationID     string                `bson:"organization_id,omitempty"`
}

const (
	webrtcCallIDField                 = "_id"
	webrtcCallCallerOperatorIDField   = "caller_operator_id"
	webrtcCallAnswererOperatorIDField = "answerer_operator_id"
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

	webrtcOperatorIDField                        = "_id"
	webrtcOperatorHostsField                     = "hosts"
	webrtcOperatorHostsHostField                 = "host"
	webrtcOperatorHostsCallerSizeField           = "caller_size"
	webrtcOperatorHostsAnswererSizeField         = "answerer_size"
	webrtcOperatorExpireAtField                  = "expire_at"
	webrtcOperatorHostsHostCombinedField         = webrtcOperatorHostsField + "." + webrtcOperatorHostsHostField
	webrtcOperatorHostsCallerSizeCombinedField   = webrtcOperatorHostsField + "." + webrtcOperatorHostsCallerSizeField
	webrtcOperatorHostsAnswererSizeCombinedField = webrtcOperatorHostsField + "." + webrtcOperatorHostsAnswererSizeField
)

type mongodbNewCallEventHandler struct {
	eventChan   chan<- mongodbCallEvent // expected buffered cap 1
	receiveOnce sync.Once
}

func (newCall *mongodbNewCallEventHandler) Send(event mongodbCallEvent, logger utils.ZapCompatibleLogger) bool {
	var sent bool
	newCall.receiveOnce.Do(func() {
		// should always work
		select {
		case newCall.eventChan <- event:
			sent = true
		default:
			logger.Infow("Hit default select in send",
				"Event", event.Call)
		}
	})
	logger.Infow("returning from send",
		"Event", event.Call,
		"Sent", sent)
	return sent
}

type mongodbCallExchange struct {
	Host string
	Chan chan<- mongodbCallEvent
	Side string // "caller" or "answerer"
}

type mongodbCallEvent struct {
	Call    mongodbWebRTCCall
	Expired bool
}

const (
	operatorStateUpdateInterval = time.Second
	operatorHeartbeatWindow     = time.Second * 15
)

// The operatorLivenessLoop keeps the distributed queue aware of this operator's existence, in
// addition to the hosts its listening to calls for, in order to keep track of eventually
// consistent queue maximums.
func (queue *mongoDBWebRTCCallQueue) operatorLivenessLoop() {
	ticker := time.NewTicker(operatorStateUpdateInterval)
	defer ticker.Stop()
	for {
		if !utils.SelectContextOrWaitChan(queue.cancelCtx, ticker.C) {
			return
		}
		type callerAnswererQueueSizes struct {
			Caller   uint64
			Answerer uint64
		}
		queue.csStateMu.RLock()
		hosts := make(map[string]callerAnswererQueueSizes, len(queue.waitingForNewCallSubs)+len(queue.callExchangeSubs))
		for host, waiting := range queue.waitingForNewCallSubs {
			sizes := hosts[host]
			sizes.Answerer += uint64(len(waiting))
			hosts[host] = sizes
		}
		for _, exchanges := range queue.callExchangeSubs {
			for exchange := range exchanges {
				sizes := hosts[exchange.Host]
				if exchange.Side == "caller" {
					sizes.Caller++
				} else {
					// when an answerer is initially done waiting, we will
					// account for it here as it becomes an exchanger.
					sizes.Answerer++
				}
				hosts[exchange.Host] = sizes
			}
		}
		queue.csStateMu.RUnlock()
		// put a time stamp in the operator to show when this was updated
		// then when the operator goes offline, we should update the robot part collection

		hostSizes := make(bson.A, 0, len(hosts))
		hostsWithAnswerers := []string{}

		for host, sizes := range hosts {
			if sizes.Answerer >= 1 {
				hostsWithAnswerers = append(hostsWithAnswerers, host)
			}

			hostSizes = append(hostSizes, bson.D{
				{webrtcOperatorHostsHostField, host},
				{webrtcOperatorHostsCallerSizeField, sizes.Caller},
				{webrtcOperatorHostsAnswererSizeField, sizes.Answerer},
			})
		}
		updateCtx, cancel := context.WithTimeout(queue.cancelCtx, operatorHeartbeatWindow/3)
		start := time.Now()
		result, err := queue.operatorsColl.UpdateOne(
			updateCtx,
			bson.D{{webrtcOperatorIDField, queue.operatorID}},
			bson.D{
				{
					"$set",
					bson.D{
						{webrtcOperatorExpireAtField, time.Now().Add(operatorHeartbeatWindow)},
						{webrtcOperatorHostsField, hostSizes},
					},
				},
			},
			// There is a chance that multiple previous updates took too long or failed and the document expired.
			// In these cases it is acceptable to upsert a new operator document.
			options.Update().SetUpsert(true),
		)
		if err != nil {
			reason := "context_canceled"
			if !errors.Is(err, context.Canceled) {
				switch {
				case errors.Is(err, context.DeadlineExceeded):
					reason = "deadline_exceeded"
				default:
					//nolint:goconst
					reason = "other"
				}
				queue.logger.Infow("failed to update operator document for self", "error", err)
			}
			operatorsCollUpdateFailures.Inc(queue.operatorID, reason)
		} else if result.UpsertedCount == 1 {
			queue.logger.Infow("no existing operator document found, inserted new operator document")
			// not technically a failure, but interesting to track
			operatorsCollUpserts.Inc(queue.operatorID)
		}
		cancel()

		// Answerer liveness is determined by whether the operator document is updated.
		// If updates to the operator document fail continuously, the answerers are
		// not live as callers will not be able to make connections to the answerers connected
		// to this queue. Therefore, if update failed, do not run the onAnswererLiveness func.
		// This errcheck is done above as well, but pulled the continue statement out
		// into a separate block for readability.
		//
		// APP-14368: if operator updates have failed continuously for some amount of time, kick off
		// all connected answerers and refuse new answerers until operator updates are healthy again.
		if err != nil {
			continue
		}
		if time.Since(start) > 3*time.Second {
			queue.logger.Infow(
				"successful update to operator document took a long time",
				"time_elapsed", time.Since(start).String(),
			)
		}

		if queue.onAnswererLiveness != nil {
			queue.onAnswererLiveness(hostsWithAnswerers, time.Now())
		}
	}
}

// The changeStreamManager is responsible for maintaining a change stream that is always updating
// its query in response to new answerers making themselves available for calls. It helps
// efficiently swap out new change streams while an old one may still be in use by the subscriptionManager.
// It also is resilient to crashes so long as the idempotency principles of the queue stay in place.
func (queue *mongoDBWebRTCCallQueue) changeStreamManager() {
	ticker := time.NewTicker(operatorStateUpdateInterval)
	defer ticker.Stop()
	defer func() {
		if queue.csCtxCancel != nil {
			queue.csCtxCancel()
		}
	}()
	var lastSeq uint64
	var isInitialized bool // this is only for the first time the changestream is setup
	for {
		// Note(erd): this could use condition variables instead in order to be efficient about
		// change stream restarts, but it does not feel worth the complexity right now :o)
		if !utils.SelectContextOrWaitChan(queue.cancelCtx, ticker.C) {
			return
		}

		queue.csStateMu.RLock()
		activeHosts.Set(queue.operatorID, int64(len(queue.waitingForNewCallSubs)))
		queue.csStateMu.RUnlock()

		currSeq := queue.csManagerSeq.Load()
		if isInitialized && lastSeq == currSeq {
			continue
		}
		lastSeq = currSeq
		isInitialized = true

		queue.csStateMu.Lock()
		hosts := make([]string, 0, len(queue.waitingForNewCallSubs))
		for host := range queue.waitingForNewCallSubs {
			hosts = append(hosts, host)
		}
		readyFuncs := make([]func(), len(queue.csAnswerersWaitingForNextCS))
		copy(readyFuncs, queue.csAnswerersWaitingForNextCS)
		queue.csAnswerersWaitingForNextCS = nil

		csOpts := options.ChangeStream().SetFullDocument(options.UpdateLookup)

		// only one can ever be set
		if len(queue.csLastResumeToken) != 0 {
			csOpts.SetStartAfter(queue.csLastResumeToken)
		}
		if !queue.csLastEventClusterTime.IsZero() {
			ctCopy := queue.csLastEventClusterTime
			csOpts.SetStartAtOperationTime(&ctCopy)
		}
		queue.csStateMu.Unlock()

		// note(roxy): this is updating the changestream based on whether there is a new
		// answerer that is coming online or if there is a new caller that is coming online
		cs, err := queue.callsColl.Watch(queue.cancelCtx, []bson.D{
			{
				{"$match", bson.D{
					{"operationType", bson.D{{"$in", []interface{}{
						mongoutils.ChangeEventOperationTypeInsert,
						mongoutils.ChangeEventOperationTypeUpdate,
						mongoutils.ChangeEventOperationTypeDelete,
						mongoutils.ChangeEventOperationTypeInvalidate, // this will be caught for us as an error
					}}}},
					// On the caller side, we want to listen for anything for this operator; so listen
					// for updates and deletes on caller_operator_id. All call updates are relevant to us
					// so there is no need to listen for call ids.
					// On the answerer side, we want to initially listen for an insert based on all relevant
					// hosts since we do not want to have the caller query for eventually consistent operator
					// liveness updates. Instead, we will assume that the hosts here changes often but that
					// the answerer in RecvOffer will check for an incoming call out of band while this
					// change stream updates and keeps itself in sync time-wise with a resume token.
					{"$or", []interface{}{
						bson.D{{fmt.Sprintf("fullDocument.%s", webrtcCallCallerOperatorIDField), queue.operatorID}},
						bson.D{{fmt.Sprintf("fullDocument.%s", webrtcCallAnswererOperatorIDField), queue.operatorID}},
						bson.D{{fmt.Sprintf("fullDocument.%s", webrtcCallHostField), bson.D{{"$in", hosts}}}},
					}},
				}},
			},
		}, csOpts)
		if err != nil {
			callChangeStreamFailures.Inc(queue.operatorID)
			queue.csManagerSeq.Add(1)
			queue.logger.Infow("failed to create calls change stream", "error", err)
			continue
		}

		for _, readyFunc := range readyFuncs {
			readyFunc()
		}
		queue.csStateMu.Lock()
		queue.csTrackingHosts = utils.NewStringSet(hosts...)
		queue.csStateMu.Unlock()

		nextCSCtx, nextCSCtxCancel := context.WithCancel(queue.cancelCtx)
		csNext, resumeToken, clusterTime := mongoutils.ChangeStreamBackground(nextCSCtx, cs)

		select {
		case <-queue.cancelCtx.Done():
			// note(roxy): this is the server's cancelCtx being called
			// should stop the entire call queue managed by CS, not just a single CS
			nextCSCtxCancel()
			return
		case queue.csStateUpdates <- changeStreamStateUpdate{
			ChangeStream: csNext,
			ResumeToken:  resumeToken,
			ClusterTime:  clusterTime,
		}:
			// close old; goroutine may linger a bit
			if queue.csCtxCancel != nil {
				queue.csCtxCancel()
			}
			queue.csCtxCancel = nextCSCtxCancel
		}
	}
}

func (queue *mongoDBWebRTCCallQueue) processClusterEventState(
	newResumeToken bson.Raw,
	newClusterTime primitive.Timestamp,
) bool {
	queue.csStateMu.Lock()
	if !newClusterTime.IsZero() {
		if queue.csLastEventClusterTime.T > newClusterTime.T ||
			(queue.csLastEventClusterTime.T == newClusterTime.T &&
				queue.csLastEventClusterTime.I >= newClusterTime.I) {
			queue.csStateMu.Unlock()
			// we have seen it; skip
			return false
		}
		// some real event happened, so make sure we start at this time, not the
		// resume token, the next time we create a change stream.
		queue.csLastEventClusterTime = newClusterTime
		queue.csLastResumeToken = nil
	} else if len(newResumeToken) != 0 {
		// otherwise no event happened and we want to start after this resume token
		queue.csLastResumeToken = newResumeToken
		queue.csLastEventClusterTime = primitive.Timestamp{}
	}
	queue.csStateMu.Unlock()
	return true
}

func (queue *mongoDBWebRTCCallQueue) processNextSubscriptionEvent(next mongoutils.ChangeEventResult, ok bool) bool {
	if !ok {
		// we do not really expect this to happen due to the order of events between
		// this manager in the changeStreamManager. So signal we need a new
		// change stream probably.
		queue.csManagerSeq.Add(1)
		return true
	}

	if next.Error != nil {
		queue.logger.Errorw("error getting next event in change stream", "error", next.Error)

		if errors.Is(next.Error, mongoutils.ErrChangeStreamInvalidateEvent) {
			queue.processClusterEventState(next.ResumeToken, primitive.Timestamp{})
		}
		// this is more likely to be some issue that is happening on MongoDB. It could
		// also be a context cancellation. Either way, we will signal we need a new
		// change stream and the next iteration of the loop will go back to normal
		// ideally. We will log though just in case.
		queue.csManagerSeq.Add(1)
		return true
	}

	// This is atomic but we still do not want to process the same event twice if
	// we are reopening a change stream and it is behind one. We rely on the idempotency of
	// updates to calls in addition to atomic call acquistion semantics to guarantee this.
	if !queue.processClusterEventState(next.ResumeToken, next.Event.ClusterTime) {
		return false
	}

	var callResp mongodbWebRTCCall
	if err := next.Event.FullDocument.Unmarshal(&callResp); err != nil {
		queue.logger.Errorw("failed to unmarshal call document", "error", err)
		return false
	}

	if callResp.Host == "" {
		queue.logger.Errorw("unexpected call with no host", "id", callResp.ID)
		return false
	}

	// This message sending pass must be as fast possible and for that reason we use select defaults.
	// In the event default cases happen, we have determined to either have hit some terminal case
	// in the exchange or too many messages have been sent. Either way we should log and monitor these.
	func() {
		queue.csStateMu.RLock()
		defer queue.csStateMu.RUnlock()

		if next.Event.OperationType == mongoutils.ChangeEventOperationTypeInsert {
			if _, ok := queue.csTrackingHosts[callResp.Host]; !ok {
				// no one connected to this operator is currently subscribed to insert
				// events for this host; skip
				// we do this because each server is listening to an event and each host only lives on one server
				// it could be on another server
				return
			}

			// note(roxy): if the host is in the csTrackingHosts it means that there was an answerer online in the last change stream
			// but there is no longer an answerer tied to the event on this server
			// this disparity happens because the changestream has not yet updated based on the dropped answerer

			answerChans := queue.waitingForNewCallSubs[callResp.Host]
			if len(answerChans) == 0 {
				queue.logger.Debugw("no answerer is around for this new call; the next answerer will find the document instead", "host", callResp.Host)
				return
			}
			event := mongodbCallEvent{Call: callResp}
			queue.logger.Infow("answerer channels for host", "host", callResp.Host, "channels size", len(answerChans))
			for answerChan := range answerChans {
				// We will send on this channel just once and it will eventually
				// unsubscribe. We are not concerned with looping over channels
				// we have already sent once on. For rationale behind this,
				// look at the comments in RecvOffer around using events. Briefly
				// though, we want to send the events as fast as possible as mentioned
				// above and cannot block on the send to see if the receiver locked
				// the document.
				if answerChan.Send(event, queue.logger) {
					return
				}
			}
			callAnswererTooBusy.Inc(queue.operatorID, callResp.Host)
			// if we get there its because none of the answer channels were able to send on the event
			queue.logger.Warnw(
				"all answerers for host too busy to answer call",
				"id", callResp.ID,
				"host", callResp.Host,
				"collection", next.Event.NS.Collection,
				"caller operator id", callResp.CallerOperatorID,
				"caller error", callResp.CallerError,
				"caller done", callResp.CallerDone,
				"answerer operator id", callResp.AnswererOperatorID,
				"answerer error", callResp.AnswererError,
				"answerer done", callResp.AnswererDone,
				"number of answer channels", len(answerChans),
			)
		}

		if next.Event.OperationType == mongoutils.ChangeEventOperationTypeUpdate ||
			next.Event.OperationType == mongoutils.ChangeEventOperationTypeDelete {
			exchangeChans := queue.callExchangeSubs[callResp.ID]
			if len(exchangeChans) == 0 {
				queue.logger.Debugw("no call exchangers remain for", "id", callResp.ID, "host", callResp.Host)
				return
			}
			var event mongodbCallEvent
			if next.Event.OperationType == mongoutils.ChangeEventOperationTypeUpdate {
				event.Call = callResp
			} else {
				event.Expired = true
			}
			for exchangeChan := range exchangeChans {
				select {
				case exchangeChan.Chan <- event:
				default:
					exchangeChannelAtCapacity.Inc(queue.operatorID)
					queue.logger.Debugw("failed to notify exchange channel of call update",
						"id", callResp.ID, "host", callResp.Host, "side", exchangeChan.Side)
				}
			}
		}
	}()

	return false
}

func (queue *mongoDBWebRTCCallQueue) subscriptionManager(currentCS <-chan mongoutils.ChangeEventResult) {
	var waitForNextCS bool
	for {
		if queue.cancelCtx.Err() != nil {
			return
		}
		if waitForNextCS {
			// we want to block here so that we do not keep receiving bad events.
			waitForNextCS = false
			select {
			case <-queue.cancelCtx.Done():
				return
			case newState := <-queue.csStateUpdates:
				currentCS = newState.ChangeStream
				continue
			}
		} else {
			// otherwise we can do a quick check.
			select {
			case <-queue.cancelCtx.Done():
				return
			case next, ok := <-currentCS: // try and make some progress at least once
				waitForNextCS = queue.processNextSubscriptionEvent(next, ok)
				if waitForNextCS { // something bad happened and we requested/need a new CS
					continue
				}
			default:
			}
		}

		// finally allow accepting a new change stream while checking for events.
		select {
		case <-queue.cancelCtx.Done():
			return
		case newState := <-queue.csStateUpdates:
			currentCS = newState.ChangeStream
			continue
		case next, ok := <-currentCS:
			waitForNextCS = queue.processNextSubscriptionEvent(next, ok)
			if waitForNextCS { // something bad happened and we requested/need a new CS
				continue
			}
		}
	}
}

// subscribeToCall allows for a caller or answerer to subscribe for events related to the given call id. It
// does not wait for any operator change stream updates since calls are attached to operator IDs that will
// always receive corresponding updates.
func (queue *mongoDBWebRTCCallQueue) subscribeToCall(host, callID, side string) (<-chan mongodbCallEvent, func()) {
	queue.csStateMu.Lock()
	defer queue.csStateMu.Unlock()

	// 50 is a very high amount of events that is unlikely to happen. If it does, we consider it an error
	// and will log/drop devents.
	exchangeChan := make(chan mongodbCallEvent, 50)
	exchangeSubs, ok := queue.callExchangeSubs[callID]
	if !ok {
		exchangeSubs = map[*mongodbCallExchange]struct{}{}
		queue.callExchangeSubs[callID] = exchangeSubs
	}
	exchange := &mongodbCallExchange{Host: host, Chan: exchangeChan, Side: side}
	exchangeSubs[exchange] = struct{}{}
	return exchangeChan, func() {
		queue.csStateMu.Lock()
		defer queue.csStateMu.Unlock()
		delete(exchangeSubs, exchange)
		if len(exchangeSubs) == 0 {
			delete(queue.callExchangeSubs, callID)
		}
	}
}

// subscribeForNewCallOnHosts allows an answerer to subscribe for new calls on any of the given hosts. It returns
// once this operator's change stream is tracking the hosts. The channel will have an event on it once
// any of the hosts receives a call and it is also routed to this subscriber.
func (queue *mongoDBWebRTCCallQueue) subscribeForNewCallOnHosts(
	ctx context.Context,
	hosts []string,
) (<-chan mongodbCallEvent, func(), error) {
	queue.csStateMu.Lock()
	subChan := make(chan mongodbCallEvent, 1)
	ready := make(chan struct{})
	callEventHandler := &mongodbNewCallEventHandler{
		eventChan: subChan,
	}

	var alreadyTrackedCount int
	for _, host := range hosts {
		// even if there are multiple subscribers, it still
		// all maps to a single host

		if _, ok := queue.csTrackingHosts[host]; ok {
			alreadyTrackedCount++
		}
		// if the host is not being tracked check if there is an answerer for it
		// if this is the first time an answerer is coming online for this host, then we
		// populate an initial map with 1
		// otherwise we just add the new subcriber to the map
		// "hosts's subscribers" and adds the new event channel with a lock around csStateMu
		//  each time this function is called there should only ever be a difference of a single answerer
		hostSubs, ok := queue.waitingForNewCallSubs[host]
		if !ok {
			hostSubs = map[*mongodbNewCallEventHandler]struct{}{}
			queue.waitingForNewCallSubs[host] = hostSubs
		}
		hostSubs[callEventHandler] = struct{}{}
	}

	unsub := func() {
		queue.csStateMu.Lock()
		defer queue.csStateMu.Unlock()
		for _, host := range hosts {
			delete(queue.waitingForNewCallSubs[host], callEventHandler)
			if len(queue.waitingForNewCallSubs[host]) == 0 {
				delete(queue.waitingForNewCallSubs, host)
			}
		}
	}

	if alreadyTrackedCount == len(hosts) {
		queue.csStateMu.Unlock()
		// there is no new call its just a new answerer for a host we already have a subscriber channel for
		return subChan, unsub, nil
	}

	queue.csAnswerersWaitingForNextCS = append(queue.csAnswerersWaitingForNextCS, func() {
		close(ready)
	})
	queue.csStateMu.Unlock()
	// this tells the changestream manager that a new answerer has come live
	// and we need to swap the changestreams
	queue.csManagerSeq.Add(1)

	select {
	case <-ctx.Done():
		// if the ctx is done then you delete all hosts internally stored as snwerers
		unsub()
		return nil, nil, ctx.Err()
	case <-ready:
		// this is executed when the ready channel is closed
		// this should be pretty instant after we increase the counter to account for the new answerer
		// this returns the new subChan and unSub for the existing answerer
		return subChan, unsub, nil
	}
}

var (
	projectStage   = bson.D{{"$project", bson.D{{webrtcOperatorHostsField, 1}, {"_id", 0}}}}
	unwindAggStage = bson.D{{"$unwind", "$" + webrtcOperatorHostsField}}
	groupAggStage  = bson.D{{"$group", bson.D{
		{"_id", "$" + webrtcOperatorHostsHostCombinedField},
		{"caller_size", bson.D{{"$sum", "$" + webrtcOperatorHostsCallerSizeCombinedField}}},
		{"answerer_size", bson.D{{"$sum", "$" + webrtcOperatorHostsAnswererSizeCombinedField}}},
	}}}
	groupAggStageAnswerers = bson.D{{"$group", bson.D{
		{"_id", "$" + webrtcOperatorHostsHostCombinedField},
		{"answerer_size", bson.D{{"$sum", "$" + webrtcOperatorHostsAnswererSizeCombinedField}}},
	}}}
)

var errTooManyConns = status.Error(codes.Unavailable, "too many connection attempts; please wait a bit and try again")

// checkHostQueueSize checks if the total number of callers to or answerers for a set of
// hosts exceeds the configured maxima (50 for callers and 4 for answerers). It does this
// by running an aggregation pipeline against the operators collection. That pipeline will
// sum `caller_size` and `answerer_size` fields across all operators for the provided
// hosts. If any of the provided hosts exceeds a total caller or answerer size maximum
// across all operators, an error is returned. Otherwise, nil is returned. Checking caller
// or answerer size is controlled via the forCaller flag.
//
// NOTE(benjirewis): I believe `len(hosts) == 1`. The hosts list is ultimately derived by
// the value of `rpc-host` passed to the `Answer` metadata from the answerer for a
// machine. For external signaling (the only signaling that will route through here) there
// is only ever one host reported and therefore included in `hosts` here: the
// `.viam.cloud` URI.
func (queue *mongoDBWebRTCCallQueue) checkHostQueueSize(ctx context.Context, forCaller bool, hosts ...string) error {
	ctx, span := trace.StartSpan(ctx, "CallQueue::checkHostQueueSize")
	defer span.End()

	hostsMatch := bson.D{
		{"$match", bson.D{{webrtcOperatorHostsHostCombinedField, bson.D{{"$in", hosts}}}}},
	}
	pipeline := []interface{}{
		hostsMatch,
		projectStage,
		unwindAggStage,
		hostsMatch,
		groupAggStage,
	}
	if forCaller {
		pipeline = append(pipeline, queue.hostCallerQueueSizeMatchAggStage)
	} else {
		pipeline = append(pipeline, queue.hostAnswererQueueSizeMatchAggStage)
	}
	cursor, err := queue.operatorsColl.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	var ret []interface{}
	if err := cursor.All(ctx, &ret); err != nil {
		return err
	}
	if len(ret) == 0 {
		return nil
	}
	return errTooManyConns
}

// ErrHostOffline is returned when a host appears to be offline.
var ErrHostOffline = status.Error(codes.NotFound, "host appears to be offline; ensure machine is online and try again")

// checkHostOnline will check if there is some operator for all the managed hosts that
// claims to have an answerer online for that host. It does this by running an aggregation
// pipeline against the operators collection to sum `answerer_size` for each host across
// all operators. If any of the provided hosts has a total answerer size of 0 or isn't
// managed by any operator, an error is returned. Otherwise, nil is returned.
//
// NOTE(benjirewis): The same NOTE about `len(hosts) == 1` applies here as in the method
// above.
func (queue *mongoDBWebRTCCallQueue) checkHostOnline(ctx context.Context, hosts ...string) error {
	ctx, span := trace.StartSpan(ctx, "CallQueue::checkHostOnline")
	defer span.End()

	hostsMatch := bson.D{
		{"$match", bson.D{{webrtcOperatorHostsHostCombinedField, bson.D{{"$in", hosts}}}}},
	}
	pipeline := []interface{}{
		hostsMatch,
		projectStage,
		unwindAggStage,
		hostsMatch,
		groupAggStageAnswerers,
		queue.hostAnswererOnlineMatchAggStage,
	}

	cursor, err := queue.operatorsColl.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	var ret []interface{}
	if err := cursor.All(ctx, &ret); err != nil {
		return err
	}
	if len(ret) == 0 {
		return ErrHostOffline
	}
	return nil
}

// Uses the passed in parameters to increment the connectionEstablishmentExpectedFailures
// metric and add attributes to the passed in span.
func (queue *mongoDBWebRTCCallQueue) incrementConnectionEstablishmentExpectedFailures(
	host, sdkType, organizationID string,
	err error,
	span *trace.Span,
) {
	// NOTE(RSDK-12228): Track "expected failures," or failures where the signaling server
	// immediately blocks a caller, with a metric. We want to keep track of when and why the
	// signaling server is making the decision to not even add the caller to the queue.

	// Reason for blocking is one of a. neither answerer for the machine currently being
	// connected to an operator, b. >= 50 callers waiting for same machine, c. some other
	// error (internal error from MDB query, e.g.). We can tell from the passed in error.

	reason := "other"
	if errors.Is(err, ErrHostOffline) {
		reason = "answerers_offline"
	} else if errors.Is(err, errTooManyConns) {
		reason = "too_many_callers"
	}

	// Check if the machine _has_ been online within the last 10s.
	onlineRecently := "unknown"
	if queue.checkAnswererLiveness != nil {
		if queue.checkAnswererLiveness(host) {
			onlineRecently = "true"
		} else {
			onlineRecently = "false"
		}
	}

	connectionEstablishmentExpectedFailures.Inc(sdkType, organizationID, reason, onlineRecently)
	span.AddAttributes(
		trace.StringAttribute("failure", "expected"),
		trace.StringAttribute("reason", reason),
		trace.StringAttribute("online_recently", onlineRecently),
	)
}

// SendOfferInit initializes an offer associated with the given SDP to the given host.
// It returns a UUID to track/authenticate the offer over time, the initial SDP for the
// sender to start its peer connection with, as well as a channel to receive candidates on
// over time.
func (queue *mongoDBWebRTCCallQueue) SendOfferInit(
	ctx context.Context,
	host, sdp string,
	disableTrickle bool,
) (string, <-chan WebRTCCallAnswer, <-chan struct{}, func(), error) {
	ctx, span := trace.StartSpan(ctx, "CallQueue::SendOfferInit")
	defer span.End()

	sdkType, organizationID := "unknown", "unknown"
	if md, exists := metadata.FromIncomingContext(ctx); exists {
		// TODO(RSDK-11864): Use actual structured metadata provided by the SDK to determine
		// SDK type for the TypeScript, Python, and C++ SDKs.
		//
		// Hackily guess the SDK type based on a combination of the "viam_client",
		// "x-grpc-web", and "user-agent" metadata fields. The first only exists for Golang,
		// the second only exists for TypeScrpt, and the third is "tonic" for both Python and
		// C++.
		if viamClientMD, exists := md[ViamClientMetadataField]; exists && len(viamClientMD) > 0 {
			if strings.Contains(viamClientMD[0], "go;") {
				sdkType = "go"
			}
		} else if xGRPCWebMD, exists := md[XGRPCWebMetadataField]; exists &&
			len(xGRPCWebMD) > 0 {
			if xGRPCWebMD[0] == "1" {
				sdkType = "typescript"
			}
		} else if userAgentMD, exists := md[UserAgentMetadataField]; exists &&
			len(userAgentMD) > 0 {
			if strings.Contains(userAgentMD[0], "tonic/") {
				sdkType = "python/c++"
			}
		}

		// TODO(RSDK-11875): Use an actual database (or, likely, cache) lookup to determine
		// the organization ID for this host based on its DNS name.
		//
		// Hackily guess the organization ID based on the "org" in the "cookie" field (only
		// present for the TypeScript SDK).
		if cookieMD, exists := md[CookieMetadataField]; exists && len(cookieMD) > 0 {
			// The "cookie" field usually has the following structure:
			// 	"session-id=[uuid]; org=[uuid]"
			// Bail if that structure is not found.
			for _, cookieField := range strings.Split(cookieMD[0], ";") {
				cookieField = strings.TrimSpace(cookieField)
				if strings.HasPrefix(cookieField, "org=") {
					organizationID = strings.TrimPrefix(cookieField, "org=")
					break
				}
			}
		}
	}
	span.AddAttributes(
		trace.StringAttribute("sdk_type", sdkType),
		trace.StringAttribute("organization_id", organizationID),
	)

	// An offer initialization (after verifying the host queue size), indicates an attempted
	// connection establishment attempt.
	connectionEstablishmentAttempts.Inc(sdkType, organizationID)

	if err := queue.checkHostQueueSize(ctx, true, host); err != nil {
		queue.incrementConnectionEstablishmentExpectedFailures(host, sdkType, organizationID, err, span)
		return "", nil, nil, nil, err
	}

	if err := queue.checkHostOnline(ctx, host); err != nil {
		queue.incrementConnectionEstablishmentExpectedFailures(host, sdkType, organizationID, err, span)

		// TODO(RSDK-11928): Implement proper time-based rate limiting to prevent clients from spamming connection attempts to offline machines so
		// we can remove sleep and error instantly.

		// Machine is offline but if we return the error instantly, clients can immediately reattempt connection establishment, overwhelming the
		// signaling server if spammed. Instead, sleep for a few seconds to slow down reattempts and give robots the chance to potentially come
		// online.
		select {
		case <-time.After(offlineHostRetryDelay):
			return "", nil, nil, nil, err
		case <-ctx.Done():
			return "", nil, nil, nil, ctx.Err()
		}
	}

	newUUID := uuid.NewString()
	call := mongodbWebRTCCall{
		ID:               newUUID,
		CallerOperatorID: queue.operatorID,
		Host:             host,
		CallerSDP:        sdp,
		SDKType:          sdkType,
		OrganizationID:   organizationID,
	}
	events, unsubscribe := queue.subscribeToCall(host, call.ID, "caller")

	offerDeadline := time.Now().Add(getDefaultOfferDeadline())
	sendCtx, sendCtxCancel := context.WithDeadline(ctx, offerDeadline)

	// need to watch before insertion to avoid a race
	sendAndQueueCtx, sendAndQueueCtxCancel := utils.MergeContext(sendCtx, queue.cancelCtx)

	cleanup := func() {
		sendAndQueueCtxCancel()
		sendCtxCancel()
		unsubscribe()
	}
	var successful bool
	defer func() {
		if successful {
			return
		}
		cleanup()
	}()

	call.StartedAt = time.Now()
	if _, err := queue.callsColl.InsertOne(sendAndQueueCtx, call); err != nil {
		return "", nil, nil, nil, err
	}

	answererResponses := make(chan WebRTCCallAnswer, 1)
	sendAnswer := func(answer WebRTCCallAnswer) bool {
		select {
		case <-sendAndQueueCtx.Done():
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
		defer cleanup()
		defer close(answererResponses)

		haveInitSDP := false
		candLen := len(call.AnswererCandidates)
		for {
			if sendAndQueueCtx.Err() != nil {
				sendAnswer(WebRTCCallAnswer{Err: sendAndQueueCtx.Err()})
				return
			}
			var next mongodbCallEvent
			select {
			case <-sendAndQueueCtx.Done():
				sendAnswer(WebRTCCallAnswer{Err: sendAndQueueCtx.Err()})
				return
			case next = <-events:
			}

			callResp := next.Call

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
	return newUUID, answererResponses, sendAndQueueCtx.Done(), sendAndQueueCtxCancel, nil
}

// SendOfferUpdate updates the offer associated with the given UUID with a newly discovered
// ICE candidate.
func (queue *mongoDBWebRTCCallQueue) SendOfferUpdate(ctx context.Context, host, uuid string, candidate webrtc.ICECandidateInit) error {
	ctx, span := trace.StartSpan(ctx, "CallQueue::SendOfferUpdate")
	defer span.End()

	updateResult, err := queue.callsColl.UpdateOne(ctx, bson.D{
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
	ctx, span := trace.StartSpan(ctx, "CallQueue::SendOfferDone")
	defer span.End()

	updateResult := queue.callsColl.FindOneAndUpdate(ctx, bson.D{
		{webrtcCallIDField, uuid},
		{webrtcCallHostField, host},
	}, bson.D{{"$set", bson.D{{webrtcCallCallerDoneField, true}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After))

	if err := updateResult.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return newInactiveOfferErr(uuid)
		}
		return err
	}

	recordExchangeDuration(updateResult)
	return nil
}

// SendOfferError informs the queue that the offer associated with the UUID has encountered
// an error from the sender side.
func (queue *mongoDBWebRTCCallQueue) SendOfferError(ctx context.Context, host, uuid string, err error) error {
	ctx, span := trace.StartSpan(ctx, "CallQueue::SendOfferError")
	defer span.End()

	updateResult := queue.callsColl.FindOneAndUpdate(ctx, bson.D{
		{webrtcCallIDField, uuid},
		{webrtcCallHostField, host},
		{webrtcCallCallerDoneField, bson.D{{"$ne", true}}},
	}, bson.D{{"$set", bson.D{{webrtcCallCallerErrorField, err.Error()}}}})
	if err := updateResult.Err(); err != nil {
		// No matching documents is indicative of an inactive offer.
		if errors.Is(err, mongo.ErrNoDocuments) {
			return newInactiveOfferErr(uuid)
		}
		return err
	}
	var updatedMDBWebRTCCall mongodbWebRTCCall
	if err := updateResult.Decode(&updatedMDBWebRTCCall); err != nil {
		return errors.Wrap(err, "could not decode document where 'caller_error' was set")
	}

	// Increment connection establishment failure counts if we are setting a
	// `caller_error` and the `answerer_error` has not already been set.
	if updatedMDBWebRTCCall.AnswererError == "" {
		connectionEstablishmentFailures.Inc(updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID)
		if errors.Is(err, context.DeadlineExceeded) {
			connectionEstablishmentCallerTimeouts.Inc(updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID)
			span.AddAttributes(trace.StringAttribute("failure", "caller_timeout"))
		} else {
			connectionEstablishmentCallerNonTimeoutErrors.Inc(updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID)
			span.AddAttributes(trace.StringAttribute("failure", "caller_non_timeout"))
		}
		duration := float64(time.Since(updatedMDBWebRTCCall.StartedAt).Milliseconds())
		callExchangeDuration.Observe(duration, updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID, exchangeFailed)
	}

	return nil
}

// RecvOffer receives the next offer for the given host. It should respond with an answer
// once a decision is made.
func (queue *mongoDBWebRTCCallQueue) RecvOffer(ctx context.Context, hosts []string) (WebRTCCallOfferExchange, error) {
	ctx, span := trace.StartSpan(ctx, "CallQueue::RecvOffer")
	defer span.End()

	if len(hosts) > 0 {
		span.AddAttributes(trace.StringAttribute("host", hosts[0]))
	}

	if err := queue.checkHostQueueSize(ctx, false, hosts...); err != nil {
		return nil, err
	}

	recvOfferCtx, recvOfferCtxCancel := utils.MergeContext(ctx, queue.cancelCtx)
	waitForNewCall := func() (mongodbWebRTCCall, bool, error) {
		events, callUnsubscribe, err := queue.subscribeForNewCallOnHosts(recvOfferCtx, hosts)
		if err != nil {
			return mongodbWebRTCCall{}, false, err
		}

		cleanup := func() {
			recvOfferCtxCancel()
			callUnsubscribe()
		}
		var recvOfferSuccessful bool
		defer func() {
			if recvOfferSuccessful {
				return
			}
			cleanup()
		}()

		// We would like to answer all calls within their deadline but there is some amount of time required to
		// connect. That estimated time to connect is subtracted off the window so that we do not grab any
		// offers that are about to expire.
		// Example:
		// An offer that starts at T=2 and expires at 12.
		// T | 1 2 3 4 5 6  7 8 9 10 11 12 13 14 15 16  17 18 19 20
		//       O         (    [        E              C]
		//       ^         ^    ^        ^               ^
		//     Start    Window  |      Expire     Check from now window bound
		//                      |
		//             Window with estimated connect time
		startedAtWindow := time.Now().Add(-getDefaultOfferDeadline()).Add(getDefaultOfferCloseToDeadline())

		// but also check first if there is anything for us.
		// first we wait to see if there is a caller waiting for us in the Callers Collection
		// If err != nil that means the doc doesn't exist yet or there is another error
		// we care if the doc doesn't yet exist
		result := queue.callsColl.FindOneAndUpdate(
			recvOfferCtx,
			bson.D{
				{webrtcCallHostField, bson.D{{"$in", hosts}}},
				{webrtcCallCallerErrorField, bson.D{{"$exists", false}}},
				{webrtcCallAnsweredField, false},
				{webrtcCallStartedAtField, bson.D{{"$gt", startedAtWindow}}},
			},
			bson.D{
				{"$set", bson.D{
					{webrtcCallAnswererOperatorIDField, queue.operatorID},
					{webrtcCallAnsweredField, true},
				}},
			})
		var callReq mongodbWebRTCCall
		err = result.Decode(&callReq)
		if err != nil {
			if !errors.Is(err, mongo.ErrNoDocuments) {
				return mongodbWebRTCCall{}, false, err
			}

			getFirstResult := func() (bool, error) {
				// bool is whether we should retry taking the offer
				if err := recvOfferCtx.Err(); err != nil {
					return false, err
				}
				select {
				case <-recvOfferCtx.Done():
					return false, recvOfferCtx.Err()
				case next := <-events:
					callReq = next.Call

					// take the offer
					result, err := queue.callsColl.UpdateOne(
						recvOfferCtx,
						bson.D{
							{webrtcCallIDField, callReq.ID},
							{webrtcCallAnsweredField, false},
						},
						bson.D{
							{"$set", bson.D{
								{webrtcCallAnswererOperatorIDField, queue.operatorID},
								{webrtcCallAnsweredField, true},
							}},
						})
					if err != nil {
						return false, err
					}
					if result.MatchedCount == 1 && result.ModifiedCount == 1 {
						// this means we have picked up the offer
						return false, nil
					}

					// Someone else took it; take it from the top. You would
					// expect you can just keep receiving on events, but since
					// the underlying change stream is shared and we do not
					// want to block often while delivering new calls, we use
					// buffered channels to deliver events meaning once this
					// receive is done over here, the channel frees up again.
					// If you look at mongodbNewCallEventHandler.Send, you will
					// see we limit the send to happening once. That means
					// if this is the only answerer at the time the new call
					// comes in and we are around here in the code, the event
					// gets dropped for the reasons just mentioned. That means
					// we need to subscribe once more but more importantly,
					// run the findOneAndUpdate again. If each caller/answerer
					// had their own change streams, the buffering would be
					// done on the MongoDB database side but that has its own
					// performance downsides that were explored in previous versions
					// of this code.
					return true, nil
				}
			}
			retry, err := getFirstResult()
			if err != nil {
				return mongodbWebRTCCall{}, false, err
			}
			if retry {
				return mongodbWebRTCCall{}, true, err
			}
		}

		recvOfferSuccessful = true
		cleanup()

		return callReq, false, nil
	}

	var callReq mongodbWebRTCCall
	for {
		var retry bool
		var err error
		callReq, retry, err = waitForNewCall()
		if err != nil {
			return nil, err
		}
		if retry {
			continue
		}
		break
	}

	events, exchangeUnsubscribe := queue.subscribeToCall(callReq.Host, callReq.ID, "answerer")

	offerDeadline := callReq.StartedAt.Add(getDefaultOfferDeadline())

	recvCtx, recvCtxCancel := utils.MergeContextWithDeadline(ctx, queue.cancelCtx, offerDeadline)

	cleanup := func() {
		recvCtxCancel()
		exchangeUnsubscribe()
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
		coll:             queue.callsColl,
		callerCandidates: make(chan webrtc.ICECandidateInit),
		callerDoneCtx:    callerDoneCtx,
		deadline:         offerDeadline,
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
	// at this point we know that there are both callers and answerers that are both live
	// and trying to connect to each other
	// as both are doing trickle ice and generating new candidates with SDPs that are being updated in the
	// table we try each of them as they come in to make a match
	queue.activeBackgroundWorkers.Add(1)
	utils.PanicCapturingGo(func() {
		defer queue.activeBackgroundWorkers.Done()
		defer callerDoneCancel()
		defer cleanup()

		candLen := len(callReq.CallerCandidates)
		latestReq := callReq
		for {
			// because of our usage of update lookup being a full document,
			// there is a chance that we get the document with enough information
			// to process the call. Therefore, we process the current info and then
			// wait for more events.

			if latestReq.CallerError != "" {
				exchange.callerErr = errors.New(latestReq.CallerError)
				return
			}

			if len(latestReq.CallerCandidates) > candLen {
				prevCandLen := candLen
				newCandLen := len(latestReq.CallerCandidates) - candLen
				candLen += newCandLen
				for i := 0; i < newCandLen; i++ {
					cand := iceCandidateFromMongo(latestReq.CallerCandidates[prevCandLen+i])
					if !sendCandidate(cand) {
						return
					}
				}
			}

			if latestReq.CallerDone {
				return
			}

			if err := recvCtx.Err(); err != nil {
				return
			}

			select {
			case <-recvCtx.Done():
				return
			case next := <-events:
				if next.Expired {
					exchange.callerErr = errors.New("offer expired")
					return
				}

				latestReq = next.Call
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

func recordExchangeDuration(updateResult *mongo.SingleResult) {
	var call mongodbWebRTCCall
	if err := updateResult.Decode(&call); err != nil {
		return
	}

	// We cannot consider an exchange finished until both sides are done
	// because a state of done indicates that a side has finished sending
	// all its candidates (or the client channel is ready). Recording on
	// the first done would capture an incomplete exchange where one side
	// has finished exchanging candidates but the other side has not, so
	// the exchange is clearly not finished. By waiting until both sides
	// are done, we ensure that we measure the full end-to-end signaling
	// process from offer creation to both sides confirming completion.
	if !call.CallerDone || !call.AnswererDone {
		return
	}

	duration := float64(time.Since(call.StartedAt).Milliseconds())
	var finalResult string
	if call.CallerError != "" || call.AnswererError != "" {
		finalResult = exchangeFailed
	} else {
		finalResult = exchangeFinished
	}
	callExchangeDuration.Observe(duration, call.SDKType, call.OrganizationID, finalResult)
}

// Close cancels all active offers and waits to cleanly close all background workers.
func (queue *mongoDBWebRTCCallQueue) Close() error {
	queue.cancelFunc()
	queue.activeBackgroundWorkers.Wait()
	return nil
}

// waitForAnswererOnline blocks until there is at least one answerer online for all the given hosts.
// Used in testing to synchronize callers and answerers so that call attempts don't immediately fail
// due to answerers not yet registered as being online.
func (queue *mongoDBWebRTCCallQueue) waitForAnswererOnline(ctx context.Context, hosts []string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		allOnline := true
		for _, host := range hosts {
			filter := bson.M{
				webrtcOperatorHostsHostCombinedField:         host,
				webrtcOperatorHostsAnswererSizeCombinedField: bson.M{"$gt": 0},
			}

			count, err := queue.operatorsColl.CountDocuments(ctx, filter)
			if err != nil {
				return err
			}
			if count == 0 {
				allOnline = false
				break
			}
		}

		if allOnline {
			return nil
		}
	}
}

type mongoDBWebRTCCallOfferExchange struct {
	call             mongodbWebRTCCall
	coll             *mongo.Collection
	callerCandidates chan webrtc.ICECandidateInit
	callerDoneCtx    context.Context
	callerErr        error
	deadline         time.Time
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

func (resp *mongoDBWebRTCCallOfferExchange) Deadline() time.Time {
	return resp.deadline
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
	ctx, span := trace.StartSpan(ctx, "CallOfferExchange::AnswererRespond")
	defer span.End()

	var toSet bson.D
	var toPush bson.D
	var answererErrorSet bool
	switch {
	case ans.InitialSDP != nil:
		toSet = append(toSet, bson.E{webrtcCallAnswererSDPField, ans.InitialSDP})
	case ans.Candidate != nil:
		toPush = append(toPush, bson.E{webrtcCallAnswererCandidatesField, iceCandidateToMongo(ans.Candidate)})
	case ans.Err != nil:
		answererErrorSet = true
		toSet = append(toSet, bson.E{webrtcCallAnswererErrorField, ans.Err.Error()})
	default:
		return errors.New("expected either SDP, ICE candidate, or error to be set")
	}

	var update bson.D
	if len(toSet) > 0 {
		update = append(update, bson.E{"$set", toSet})
	}
	if len(toPush) > 0 {
		update = append(update, bson.E{"$push", toPush})
	}
	if len(update) == 0 {
		return nil
	}

	updateResult := resp.coll.FindOneAndUpdate(
		ctx,
		bson.D{
			{webrtcCallIDField, resp.call.ID},
		},
		update,
	)
	if err := updateResult.Err(); err != nil {
		// No matching documents is indicative of an inactive offer.
		if errors.Is(err, mongo.ErrNoDocuments) {
			return newInactiveOfferErr(resp.call.ID)
		}
		return err
	}
	if answererErrorSet {
		var updatedMDBWebRTCCall mongodbWebRTCCall
		if err := updateResult.Decode(&updatedMDBWebRTCCall); err != nil {
			return errors.Wrap(err, "could not decode document where 'answerer_error' was set")
		}

		// Increment connection establishment failure counts if we are setting an
		// `answerer_error` and the `caller_error` has not already been set.
		if updatedMDBWebRTCCall.CallerError == "" {
			connectionEstablishmentFailures.Inc(updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID)
			if errors.Is(ans.Err, context.DeadlineExceeded) {
				connectionEstablishmentAnswererTimeouts.Inc(updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID)
				span.AddAttributes(trace.StringAttribute("failure", "answerer_timeout"))
			} else {
				connectionEstablishmentAnswererNonTimeoutErrors.Inc(updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID)
				span.AddAttributes(trace.StringAttribute("failure", "answerer_non_timeout"))
			}
			duration := float64(time.Since(updatedMDBWebRTCCall.StartedAt).Milliseconds())
			callExchangeDuration.Observe(duration, updatedMDBWebRTCCall.SDKType, updatedMDBWebRTCCall.OrganizationID, exchangeFailed)
		}
	}

	return nil
}

func (resp *mongoDBWebRTCCallOfferExchange) AnswererDone(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "CallOfferExchange::AnswererDone")
	defer span.End()

	updateResult := resp.coll.FindOneAndUpdate(ctx, bson.D{
		{webrtcCallIDField, resp.UUID()},
		{webrtcCallHostField, resp.call.Host},
	}, bson.D{{"$set", bson.D{{webrtcCallAnswererDoneField, true}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After))

	if err := updateResult.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return newInactiveOfferErr(resp.call.ID)
		}
		return err
	}

	recordExchangeDuration(updateResult)
	return nil
}
