package statz

import (
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/perf/statz/statztest"
	"go.viam.com/utils/perf/statz/units"
)

func TestGauge(t *testing.T) {
	gauge1 := NewGauge1[string]("statz/test/gauge1", MetricConfig{
		Description: "The number of requests",
		Unit:        units.Dimensionless,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
		},
	})

	gauge2 := NewGauge2[string, bool]("statz/test/gauge2", MetricConfig{
		Description: "The number of requests",
		Unit:        units.Dimensionless,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
			{Name: "bool", Description: "Other label"},
		},
	})

	t.Run("gauge1", func(t *testing.T) {
		recorder := statztest.NewGaugeRecorder("statz/test/gauge1")

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 0)

		gauge1.Set("label1", 5)
		gauge1.Set("label2", 6)

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 5)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 6)

		gauge1.Set("label1", 1)
		gauge1.Set("label2", 2)

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 1)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 2)
	})

	t.Run("gauge2", func(t *testing.T) {
		recorder := statztest.NewGaugeRecorder("statz/test/gauge2")

		test.That(t, recorder.Value("label", "v1", "bool", "true"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "v1", "bool", "false"), test.ShouldEqual, 0)

		gauge2.Set("v1", true, 10)
		gauge2.Set("v1", false, 1)

		test.That(t, recorder.Value("label", "v1", "bool", "true"), test.ShouldEqual, 10)
		test.That(t, recorder.Value("label", "v1", "bool", "false"), test.ShouldEqual, 1)
	})
}
