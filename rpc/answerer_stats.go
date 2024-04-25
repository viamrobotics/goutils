package rpc

import (
	"fmt"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
)

// timeFormatStr copied from DefaultTimeFormatStr in RDK.
const timeFormatStr = "2006-01-02T15:04:05.000Z0700"

// answererStats is a collection of measurements/information gathered during
// the course of a single connection establishment attempt from a signaling
// answerer. We log the contents of the struct as a single INFO log message
// upon successful or failed connection establishment to avoid emitting dozens
// of logs during the answering process and cluttering regular robot logs.
// Answerer stats are only logged in production for external signalers.
type answererStats struct {
	// mu guards all fields on answererStats.
	mu sync.Mutex

	success           bool
	totalAnswerUpdate time.Duration

	// Stats below will be logged.
	answerRequestInitReceived *time.Time
	numAnswerUpdates          int
	averageAnswerUpdate       time.Duration
	maxAnswerUpdate           time.Duration
	localICECandidates        []*localICECandidate
	remoteICECandidates       []*remoteICECandidate
}

// logs the answerer stats if connection establishment was successful or there
// was a clear failure: !success && as.AnswerRequestInitReceived != nil. If
// !success && as.AnswerRequestInitReceived == nil, another answerer picked up
// the connection establishment attempt. Cannot be called while holding mutex.
func (as *answererStats) log(logger golog.Logger) {
	as.mu.Lock()
	defer as.mu.Unlock()

	msg := "Connection establishment succeeded"
	if !as.success {
		msg = "Connection establishment failed"
		if as.answerRequestInitReceived == nil {
			return
		}
	}

	var fields []any
	if as.answerRequestInitReceived != nil {
		fAnswerRequestInitReceived := as.answerRequestInitReceived.Format(timeFormatStr)
		fields = append(fields, "answerRequestInitReceieved", fAnswerRequestInitReceived)
	}
	fields = append(fields, "numAnswerUpdates", as.numAnswerUpdates)
	fields = append(fields, "averageAnswerUpdate", as.averageAnswerUpdate)
	fields = append(fields, "maxAnswerUpdate", as.maxAnswerUpdate)

	var lics, rics []string
	for _, lic := range as.localICECandidates {
		lics = append(lics, lic.String())
	}
	for _, ric := range as.remoteICECandidates {
		rics = append(rics, ric.String())
	}
	fields = append(fields, "localICECandidates", lics)
	fields = append(fields, "remoteICECandidates", rics)

	logger.Infow(msg, fields...)
}

type localICECandidate struct {
	gatheredAt time.Time
	candidate  *webrtc.ICECandidate
}

func (lic *localICECandidate) String() string {
	fGatheredAt := lic.gatheredAt.Format(timeFormatStr)
	return fmt.Sprintf("at: %v, candidate: %s", fGatheredAt, lic.candidate)
}

type remoteICECandidate struct {
	receivedAt time.Time
	candidate  *webrtc.ICECandidateInit
}

func (ric *remoteICECandidate) String() string {
	fReceivedAt := ric.receivedAt.Format(timeFormatStr)
	return fmt.Sprintf("at: %v, candidate: %v", fReceivedAt, ric.candidate.Candidate)
}
