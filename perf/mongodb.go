package perf

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.opencensus.io/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"go.viam.com/utils"
)

func registerMongoDBViews() error {
	// TODO(erd): add views
	return nil
}

// from https://github.com/entropyx/mongo-opencensus

type config struct {
	sampler trace.Sampler
}

// MongoDBMonitorOption represents an option that can be passed to NewMongoDBMonitor.
type MongoDBMonitorOption func(*config)

// WithMongoDBMonitorSampler set a sampler for all started spans.
func WithMongoDBMonitorSampler(sampler trace.Sampler) MongoDBMonitorOption {
	return func(cfg *config) {
		cfg.sampler = sampler
	}
}

type spanKey struct {
	ConnectionID string
	RequestID    int64
}

type monitor struct {
	sync.Mutex
	spans map[spanKey]*trace.Span
	cfg   *config
}

func (m *monitor) Started(ctx context.Context, evt *event.CommandStartedEvent) {
	connString := connectionString(evt)
	b, err := bson.MarshalExtJSON(evt.Command, false, false)
	if err != nil {
		utils.UncheckedError(err)
		return
	}
	_, span := trace.StartSpan(ctx, evt.CommandName, trace.WithSampler(m.cfg.sampler))
	span.AddAttributes(trace.StringAttribute(ext.DBInstance, evt.DatabaseName),
		trace.StringAttribute("db.system", "mongodb"),
		trace.StringAttribute("db.operation", string(b)),
		trace.StringAttribute("db.connection_string", connString),
	)
	key := spanKey{
		ConnectionID: evt.ConnectionID,
		RequestID:    evt.RequestID,
	}
	m.Lock()
	m.spans[key] = span
	m.Unlock()
}

func (m *monitor) Succeeded(ctx context.Context, evt *event.CommandSucceededEvent) {
	m.Finished(&evt.CommandFinishedEvent, nil)
}

func (m *monitor) Failed(ctx context.Context, evt *event.CommandFailedEvent) {
	m.Finished(&evt.CommandFinishedEvent, fmt.Errorf("%s", evt.Failure))
}

func (m *monitor) Finished(evt *event.CommandFinishedEvent, err error) {
	key := spanKey{
		ConnectionID: evt.ConnectionID,
		RequestID:    evt.RequestID,
	}
	m.Lock()
	span, ok := m.spans[key]
	if ok {
		delete(m.spans, key)
	}
	m.Unlock()
	if !ok {
		return
	}
	if err != nil {
		span.AddAttributes(trace.StringAttribute("error.msg", err.Error()))
	}
	span.End()
}

// NewMongoDBMonitor creates a new mongodb event CommandMonitor.
func NewMongoDBMonitor(opts ...MongoDBMonitorOption) *event.CommandMonitor {
	cfg := new(config)
	for _, opt := range opts {
		opt(cfg)
	}
	m := &monitor{
		spans: make(map[spanKey]*trace.Span),
		cfg:   cfg,
	}
	return &event.CommandMonitor{
		Started:   m.Started,
		Succeeded: m.Succeeded,
		Failed:    m.Failed,
	}
}

func connectionString(evt *event.CommandStartedEvent) string {
	hostname := evt.ConnectionID
	port := "27017"
	if idx := strings.IndexByte(hostname, '['); idx >= 0 {
		hostname = hostname[:idx]
	}
	if idx := strings.IndexByte(hostname, ':'); idx >= 0 {
		port = hostname[idx+1:]
		hostname = hostname[:idx]
	}
	return hostname + ":" + port
}
