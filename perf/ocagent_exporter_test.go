package perf

import (
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
)

func TestNewOCAgentExporterRequiresAddress(t *testing.T) {
	logger := golog.NewTestLogger(t)
	_, err := NewOCAgentExporter(OCAgentOptions{Logger: logger})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "OC_AGENT_ADDRESS")
}

func TestNewOCAgentExporterAddressFromEnv(t *testing.T) {
	logger := golog.NewTestLogger(t)
	t.Setenv("OC_AGENT_ADDRESS", "localhost:55678")
	exp, err := NewOCAgentExporter(OCAgentOptions{Logger: logger})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, exp, test.ShouldNotBeNil)
}

func TestNewOCAgentExporterAddressFromOptions(t *testing.T) {
	logger := golog.NewTestLogger(t)
	exp, err := NewOCAgentExporter(OCAgentOptions{Logger: logger, Address: "localhost:55678"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, exp, test.ShouldNotBeNil)
}
