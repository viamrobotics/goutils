package rpc

import "time"

// answererStats is a collection of measurements/information gathered during
// the lifetime of a single answerer within a webrtcSignalingAnswerer. We
// convert the stats to JSON and log the JSON representation of the struct as a
// single log message upon successful or failed connection establishment to
// avoid emitting dozens of during the answering process and cluttering robot
// logs. Answerer stats are only logged in production for external signalers.
type answererStats struct {
	// AnswerRquestInitReceived represents when the `AnswerRequest_Init` was
	// received for this connection establishment attempt. nil if none was ever
	// received (another answerer picked up the attempt, or there was an error in
	// signaling before an answerer received an init).
	AnswerRequestInitReceived *time.Time `json:"answer_request_init_received,omitempty"`
	// NumAnswerUpdates is the number of updates the answerer made to the
	// signaling server.
	NumAnswerUpdates int `json:"num_answer_updates,omitempty"`
	// AverageAnswerUpdate is the average duration for `AnswerResponse_Update`s
	// across NumAnswerUpdates.
	AverageAnswerUpdate time.Duration `json:"average_answer_update,omitempty"`
	// MaxAnswerUpdate is the greatest recorded duration for `AnswerResponseUpdate`s.
	MaxAnswerUpdate time.Duration `json:"max_answer_update,omitempty"`
	// LocalICECandidates is a slice of all gathered local ICE candidates.
	LocalICECandidates []*localICECandidate `json:"local_ice_candidates,omitempty"`
	// RemoteICECandidates is a slice of all received remote ICE candidates.
	RemoteICECandidates []*remoteICECandidate `json:"remote_ice_candidates,omitempty"`

	success           bool
	totalAnswerUpdate time.Duration
}

type localICECandidate struct {
	// GatheredAt is the time this candidate appeared in the `OnICECandidate`
	// callback.
	GatheredAt time.Time `json:"gathered_at"`
	// Candidate is a stringified version of the ICE candidate.
	Candidate string `json:"candidate"`
}

type remoteICECandidate struct {
	// ReceivedAt is the time this candidate was received from the signaling
	// server.
	ReceivedAt time.Time `json:"received_at"`
	// Candidate is a stringified version of the ICE candidate.
	Candidate string `json:"candidate"`
}
