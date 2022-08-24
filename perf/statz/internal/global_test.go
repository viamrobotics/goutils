package internal

import (
	"testing"

	"go.viam.com/test"
)

// ExampleNewGrpcStatsHandler shows how to create a new gRPC server with intrumentation for metrics/spans.
func TestRegisterMetric(t *testing.T) {
	RegisterMetric("metric/1")

	test.That(t, func() { RegisterMetric("metric/1") }, test.ShouldPanic)
}
