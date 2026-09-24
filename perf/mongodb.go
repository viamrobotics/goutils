package perf

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.opencensus.io/trace"

	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

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
	attrs := []trace.Attribute{
		trace.StringAttribute("db.system", "mongodb"),
		trace.StringAttribute("db.name", evt.DatabaseName),
		trace.StringAttribute("db.operation", evt.CommandName),
		trace.StringAttribute("db.connection_string", connString),
	}
	var collStr string
	if cmdVal, err := evt.Command.LookupErr(evt.CommandName); err == nil {
		if str, ok := cmdVal.StringValueOK(); ok {
			collStr = str
			attrs = append(attrs, trace.StringAttribute("db.mongodb.collection", collStr))
		}
	}
	var spanName string
	if collStr == "" {
		spanName = fmt.Sprintf("%s::%s", evt.DatabaseName, evt.CommandName)
	} else {
		spanName = fmt.Sprintf("%s::%s::%s", evt.DatabaseName, collStr, evt.CommandName)
	}
	_, span := trace.StartSpan(ctx, spanName, trace.WithSampler(m.cfg.sampler))
	span.AddAttributes(attrs...)
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

// Driver fallbacks when the option is unset (mongo/client.go, topology/pool.go), so the pool
// config gauge reports what the pool enforces rather than "unset".
const (
	driverDefaultMaxPoolSize   = 100
	driverDefaultMinPoolSize   = 0
	driverDefaultMaxConnecting = 2
)

const (
	poolStateWaiting    = "total_waiting_to_check_out"
	poolStateCheckedOut = "total_checked_out"
	poolStateCreated    = "total_created"
	// Label value for pool monitors created without a client name.
	defaultMongoClientName = "default"
)

var (
	// Kept for dashboards built on it; values are per address, like mongodbPoolStateGauge.
	mongodbConnectionPoolStates = statz.NewGauge2[string, string]("mongodb/connections", statz.MetricConfig{
		Description: "MongoDB connection pool state, counted per server address",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "connection_string", Description: "The replica set member this pool connects to"},
			{Name: "state", Description: "total_waiting_to_check_out / total_checked_out / total_created"},
		},
	})

	mongodbPoolStateGauge = statz.NewGauge3[string, string, string]("mongo_connection_pool_state", statz.MetricConfig{
		Description: "MongoDB connection pool state, counted per server address",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "client_name", Description: "The name the caller gave the MongoDB client"},
			{Name: "address", Description: "The replica set member this pool connects to"},
			{Name: "state", Description: "total_waiting_to_check_out / total_checked_out / total_created"},
		},
	})

	mongodbCheckoutWaitDistribution = statz.NewDistribution1[string]("mongo_connection_checkout_wait_ms", statz.MetricConfig{
		Description: "Time a request spent waiting to check out a pooled MongoDB connection",
		Unit:        units.Milliseconds,
		Labels: []statz.Label{
			{Name: "client_name", Description: "The name the caller gave the MongoDB client"},
		},
	},
		// Sub-millisecond bounds: a checkout off a warm pool is microseconds, so whole-ms buckets
		// would put all healthy traffic in one bucket and hide a regression short of saturation.
		statz.DistributionFromBounds(0, 0.1, 0.25, 0.5, 1, 5, 10, 25, 50, 100, 250, 1000, 5000),
	)

	mongodbHandshakeDistribution = statz.NewDistribution1[string]("mongo_connection_handshake_ms", statz.MetricConfig{
		Description: "Time to establish one pooled MongoDB connection (TCP, TLS and SCRAM auth)",
		Unit:        units.Milliseconds,
		Labels: []statz.Label{
			{Name: "client_name", Description: "The name the caller gave the MongoDB client"},
		},
	},
		statz.DistributionFromBounds(0, 1, 5, 10, 25, 50, 100, 250, 1000, 5000),
	)

	mongodbEstablishedCounter = statz.NewCounter1[string]("mongo_connection_established", statz.MetricConfig{
		Description: "The number of MongoDB connections that completed their handshake",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "client_name", Description: "The name the caller gave the MongoDB client"},
		},
	})

	mongodbCheckoutFailureCounter = statz.NewCounter2[string, string]("mongo_connection_checkout_failure", statz.MetricConfig{
		Description: "The number of failed MongoDB connection checkouts",
		Unit:        units.Dimensionless,
		Labels: []statz.Label{
			{Name: "client_name", Description: "The name the caller gave the MongoDB client"},
			{Name: "reason", Description: "Driver-supplied failure reason (timeout, connectionError, poolClosed)"},
		},
	})

	mongodbPoolConfigGauge = statz.NewGauge2[string, string]("mongo_pool_config", statz.MetricConfig{
		Description: "Effective MongoDB pool settings after the connection string is applied, " +
			"with driver defaults substituted where unset",
		Unit: units.Dimensionless,
		Labels: []statz.Label{
			{Name: "client_name", Description: "The name the caller gave the MongoDB client"},
			{Name: "setting", Description: "max_pool_size / min_pool_size / max_connecting"},
		},
	})
)

// NewMongoDBPoolMonitor creates a pool event PoolMonitor for a client with no name of its own.
func NewMongoDBPoolMonitor() *event.PoolMonitor {
	return NewNamedMongoDBPoolMonitor(defaultMongoClientName)
}

// NewNamedMongoDBPoolMonitor creates a pool event PoolMonitor that reports pool state per server
// address, checkout wait and failures, and connection handshake time, all labelled by clientName.
func NewNamedMongoDBPoolMonitor(clientName string) *event.PoolMonitor {
	var (
		mu         sync.Mutex
		waiting    = map[string]int64{}
		checkedOut = map[string]int64{}
		created    = map[string]int64{}
	)
	set := func(counts map[string]int64, address, state string, delta int64) {
		mu.Lock()
		defer mu.Unlock()
		counts[address] += delta
		mongodbPoolStateGauge.Set(clientName, address, state, counts[address])
		mongodbConnectionPoolStates.Set(address, state, counts[address])
	}

	return &event.PoolMonitor{
		Event: func(e *event.PoolEvent) {
			switch e.Type {
			case event.GetStarted:
				set(waiting, e.Address, poolStateWaiting, 1)
			case event.GetSucceeded:
				set(waiting, e.Address, poolStateWaiting, -1)
				set(checkedOut, e.Address, poolStateCheckedOut, 1)
				mongodbCheckoutWaitDistribution.Observe(float64(e.Duration.Microseconds())/1000, clientName)
			case event.GetFailed:
				set(waiting, e.Address, poolStateWaiting, -1)
				mongodbCheckoutFailureCounter.Inc(clientName, e.Reason)
			case event.ConnectionReturned:
				set(checkedOut, e.Address, poolStateCheckedOut, -1)
			case event.ConnectionCreated:
				set(created, e.Address, poolStateCreated, 1)
			case event.ConnectionReady:
				mongodbHandshakeDistribution.Observe(float64(e.Duration.Microseconds())/1000, clientName)
				mongodbEstablishedCounter.Inc(clientName)
			case event.ConnectionClosed:
				set(created, e.Address, poolStateCreated, -1)
			}
		},
	}
}

// RecordMongoDBPoolConfig publishes the pool settings the driver will enforce for clientName.
// Call it after ApplyURI, which can set any of these from the connection string.
func RecordMongoDBPoolConfig(clientName string, opts *options.ClientOptions) {
	valueOr := func(v *uint64, fallback int64) int64 {
		if v == nil {
			return fallback
		}
		return int64(*v)
	}
	mongodbPoolConfigGauge.Set(clientName, "max_pool_size", valueOr(opts.MaxPoolSize, driverDefaultMaxPoolSize))
	mongodbPoolConfigGauge.Set(clientName, "min_pool_size", valueOr(opts.MinPoolSize, driverDefaultMinPoolSize))
	mongodbPoolConfigGauge.Set(clientName, "max_connecting", valueOr(opts.MaxConnecting, driverDefaultMaxConnecting))
}
