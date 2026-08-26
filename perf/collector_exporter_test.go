package perf

import (
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
)

func TestNewCollectorExporterRequiresAddress(t *testing.T) {
	logger := golog.NewTestLogger(t)
	_, err := NewCollectorExporter(CollectorOptions{Logger: logger})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "OC_AGENT_ADDRESS")
}

func TestNewCollectorExporterAddressFromEnv(t *testing.T) {
	logger := golog.NewTestLogger(t)
	t.Setenv("OC_AGENT_ADDRESS", "localhost:4317")
	exp, err := NewCollectorExporter(CollectorOptions{Logger: logger})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, exp, test.ShouldNotBeNil)
}

func TestNewCollectorExporterAddressFromOptions(t *testing.T) {
	logger := golog.NewTestLogger(t)
	exp, err := NewCollectorExporter(CollectorOptions{Logger: logger, Address: "localhost:4317"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, exp, test.ShouldNotBeNil)
}
