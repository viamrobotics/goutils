package perf

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.viam.com/test"

	"go.viam.com/utils/perf/statz/statztest"
)

func TestNamedMongoDBPoolMonitor(t *testing.T) {
	state := statztest.NewGaugeRecorder("mongo_connection_pool_state")
	legacy := statztest.NewGaugeRecorder("mongodb/connections")
	wait := statztest.NewDistributionRecorder("mongo_connection_checkout_wait_ms")
	handshake := statztest.NewDistributionRecorder("mongo_connection_handshake_ms")
	established := statztest.NewCounterRecorder("mongo_connection_established")
	failures := statztest.NewCounterRecorder("mongo_connection_checkout_failure")

	monitor := NewNamedMongoDBPoolMonitor("test-client")
	emit := func(typ, address string, d time.Duration, reason string) {
		monitor.Event(&event.PoolEvent{Type: typ, Address: address, Duration: d, Reason: reason})
	}

	// Two addresses: counts must stay separate rather than one client-wide total.
	emit(event.ConnectionCreated, "a:27017", 0, "")
	emit(event.ConnectionReady, "a:27017", 12*time.Millisecond, "")
	emit(event.ConnectionCreated, "b:27017", 0, "")
	emit(event.GetStarted, "a:27017", 0, "")
	emit(event.GetStarted, "b:27017", 0, "")
	emit(event.GetSucceeded, "a:27017", 250*time.Microsecond, "")
	emit(event.GetFailed, "b:27017", 0, event.ReasonTimedOut)

	test.That(t, state.Value("client_name", "test-client", "address", "a:27017", "state", poolStateCheckedOut), test.ShouldEqual, int64(1))
	test.That(t, state.Value("client_name", "test-client", "address", "a:27017", "state", poolStateWaiting), test.ShouldEqual, int64(0))
	test.That(t, state.Value("client_name", "test-client", "address", "b:27017", "state", poolStateWaiting), test.ShouldEqual, int64(0))
	test.That(t, state.Value("client_name", "test-client", "address", "b:27017", "state", poolStateCheckedOut), test.ShouldEqual, int64(0))
	test.That(t, state.Value("client_name", "test-client", "address", "a:27017", "state", poolStateCreated), test.ShouldEqual, int64(1))
	test.That(t, legacy.Value("connection_string", "b:27017", "state", poolStateCreated), test.ShouldEqual, int64(1))

	test.That(t, wait.Value("client_name", "test-client").Count, test.ShouldEqual, int64(1))
	test.That(t, wait.Value("client_name", "test-client").Sum, test.ShouldAlmostEqual, 0.25)
	test.That(t, handshake.Value("client_name", "test-client").Count, test.ShouldEqual, int64(1))
	test.That(t, handshake.Value("client_name", "test-client").Sum, test.ShouldAlmostEqual, 12)
	test.That(t, established.Value("client_name", "test-client"), test.ShouldEqual, int64(1))
	test.That(t, failures.Value("client_name", "test-client", "reason", event.ReasonTimedOut), test.ShouldEqual, int64(1))

	emit(event.ConnectionReturned, "a:27017", 0, "")
	emit(event.ConnectionClosed, "a:27017", 0, "")
	test.That(t, state.Value("client_name", "test-client", "address", "a:27017", "state", poolStateCheckedOut), test.ShouldEqual, int64(0))
	test.That(t, state.Value("client_name", "test-client", "address", "a:27017", "state", poolStateCreated), test.ShouldEqual, int64(0))
}

func TestRecordMongoDBPoolConfig(t *testing.T) {
	config := statztest.NewGaugeRecorder("mongo_pool_config")

	RecordMongoDBPoolConfig("defaults", options.Client())
	test.That(t, config.Value("client_name", "defaults", "setting", "max_pool_size"), test.ShouldEqual, int64(driverDefaultMaxPoolSize))
	test.That(t, config.Value("client_name", "defaults", "setting", "min_pool_size"), test.ShouldEqual, int64(driverDefaultMinPoolSize))
	test.That(t, config.Value("client_name", "defaults", "setting", "max_connecting"), test.ShouldEqual, int64(driverDefaultMaxConnecting))

	RecordMongoDBPoolConfig("from-uri", options.Client().ApplyURI("mongodb://localhost/?maxPoolSize=40&minPoolSize=5&maxConnecting=8"))
	test.That(t, config.Value("client_name", "from-uri", "setting", "max_pool_size"), test.ShouldEqual, int64(40))
	test.That(t, config.Value("client_name", "from-uri", "setting", "min_pool_size"), test.ShouldEqual, int64(5))
	test.That(t, config.Value("client_name", "from-uri", "setting", "max_connecting"), test.ShouldEqual, int64(8))
}
