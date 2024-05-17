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

	counter5 := NewCounter5[string, string, string, string, string]("statz/test/counter5",
		MetricConfig{
			Description: "The number of requests",
			Unit:        units.Dimensionless,
			Labels: []Label{
				{Name: "label1", Description: "Label1"},
				{Name: "label2", Description: "Other label2"},
				{Name: "label3", Description: "Other label3"},
				{Name: "label4", Description: "Other label4"},
				{Name: "label5", Description: "Other label5"},
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

		counter1.IncBy("label1", 10)

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 11)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 1)

		counter1.IncBy("label1", -10)

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 11)
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

	t.Run("counter5", func(t *testing.T) {
		recorder := statztest.NewCounterRecorder("statz/test/counter5")

		test.That(t, recorder.Value("label1", "a", "label2", "a", "label3", "a", "label4", "a", "label5", "a"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label1", "a", "label2", "a", "label3", "a", "label4", "a", "label5", "z"), test.ShouldEqual, 0)

		counter5.IncBy("a", "a", "a", "a", "a", 10)
		counter5.Inc("a", "a", "a", "a", "z")

		test.That(t, recorder.Value("label1", "a", "label2", "a", "label3", "a", "label4", "a", "label5", "a"), test.ShouldEqual, 10)
		test.That(t, recorder.Value("label1", "a", "label2", "a", "label3", "a", "label4", "a", "label5", "z"), test.ShouldEqual, 1)
	})
}
