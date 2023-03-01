package statz

import (
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/perf/statz/statztest"
	"go.viam.com/utils/perf/statz/units"
)

func TestSummation(t *testing.T) {
	summation1 := NewSummation1[string]("statz/test/summation1", MetricConfig{
		Description: "The number of requests",
		Unit:        units.Dimensionless,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
		},
	})

	summation2 := NewSummation2[string, bool]("statz/test/summation2", MetricConfig{
		Description: "The number of requests",
		Unit:        units.Dimensionless,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
			{Name: "bool", Description: "Other label"},
		},
	})

	t.Run("summation1", func(t *testing.T) {
		recorder := statztest.NewSummationRecorder("statz/test/summation1")

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 0)

		summation1.Inc("label1")
		summation1.Inc("label2")

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 1)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 1)

		summation1.IncBy("label1", 10)

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 11)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 1)

		summation1.IncBy("label1", 10)

		test.That(t, recorder.Value("label", "label1"), test.ShouldEqual, 21)
		test.That(t, recorder.Value("label", "label2"), test.ShouldEqual, 1)
	})

	t.Run("summation2", func(t *testing.T) {
		recorder := statztest.NewSummationRecorder("statz/test/summation2")

		test.That(t, recorder.Value("label", "v1", "bool", "true"), test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "v1", "bool", "false"), test.ShouldEqual, 0)

		summation2.IncBy("v1", true, 10)
		summation2.Inc("v1", false)

		test.That(t, recorder.Value("label", "v1", "bool", "true"), test.ShouldEqual, 10)
		test.That(t, recorder.Value("label", "v1", "bool", "false"), test.ShouldEqual, 1)
	})
}
