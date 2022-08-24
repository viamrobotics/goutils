package statztest

import (
	"testing"

	"go.viam.com/test"
)

func TestCounter(t *testing.T) {
	testRecorder := NewCounterRecorder("example/recorder/metric")
	testRecorder.Value()
	test.That(t, func() { testRecorder.Value("test") }, test.ShouldPanic)
}
