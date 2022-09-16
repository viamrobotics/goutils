package statz

import (
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/perf/statz/statztest"
	"go.viam.com/utils/perf/statz/units"
)

func TestDistribution(t *testing.T) {
	distributionTest1 := NewDistribution1[string]("statz/test/distribution1", MetricConfig{
		Description: "The latency of the upload",
		Unit:        units.Milliseconds,
		Labels: []Label{
			{Name: "label", Description: "The data type (file|binary|tabular)."},
		},
	}, DistributionFromBounds(0, 10, 50))

	t.Run("distribution1", func(t *testing.T) {
		recorder := statztest.NewDistributionRecorder("statz/test/distribution1")

		test.That(t, recorder.Value("label", "label1").Sum, test.ShouldEqual, 0)
		test.That(t, recorder.Value("label", "label2").Sum, test.ShouldEqual, 0)

		distributionTest1.Observe(100, "label1")
		distributionTest1.Observe(105, "label2")
		distributionTest1.Observe(5, "label2")

		test.That(t, recorder.Value("label", "label1").Sum, test.ShouldEqual, 100)
		test.That(t, recorder.Value("label", "label2").Sum, test.ShouldEqual, 110)
		test.That(t, recorder.Value("label", "label2").Count, test.ShouldEqual, 2)
		test.That(t, recorder.Value("label", "label2").Buckets[0].Count, test.ShouldEqual, 1)
		test.That(t, recorder.Value("label", "label2").Buckets[2].Count, test.ShouldEqual, 1)
	})
}
