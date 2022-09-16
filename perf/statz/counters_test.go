package statz

import (
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/perf/statz/statztest"
	"go.viam.com/utils/perf/statz/units"
)

func TestCounter(t *testing.T) {
	counter1 := NewCounter1[string]("statz/test/counter1", MetricConfig{
		Description: "The number of requests",
		Unit:        units.Dimensionless,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
		},
	})

	counter2 := NewCounter2[string, bool]("statz/test/counter2", MetricConfig{
		Description: "The number of requests",
		Unit:        units.Dimensionless,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
			{Name: "bool", Description: "Other label"},
		},
	})

	t.Run("counter1", func(t *testing.T) {
		recorder := statztest.NewCounterRecorder("statz/test/counter1")

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 0)

		counter1.Inc("label1")
		counter1.Inc("label2")

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 1)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 1)
	})

	t.Run("counter2", func(t *testing.T) {
		recorder := statztest.NewCounterRecorder("statz/test/counter2")

		test.That(t, recorder.Value("label", "v1", "bool", "true"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "v1", "bool", "false"), test.ShouldEqual, 0)

		counter2.IncBy("v1", true, 10)
		counter2.Inc("v1", false)

		test.That(t, recorder.Value("label", "v1", "bool", "true"), test.ShouldEqual, 10)
		test.That(t, recorder.Value("label", "v1", "bool", "false"), test.ShouldEqual, 1)
	})
}
