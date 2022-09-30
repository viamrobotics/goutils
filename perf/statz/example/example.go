// Package main is an example of application stats collection
package main

import (
	"math/rand"
	"time"

	"github.com/edaniels/golog"

	"go.viam.com/utils/perf"
	"go.viam.com/utils/perf/statz"
	"go.viam.com/utils/perf/statz/units"
)

var uploadCounter = statz.NewCounter2[string, bool]("datasync/uploaded", statz.MetricConfig{
	Description: "The number of requests",
	Unit:        units.Dimensionless,
	Labels: []statz.Label{
		{Name: "type", Description: "The data type (file|binary|tabular)."},
		{Name: "status", Description: "If the upload was Successful."},
	},
})

var uploadLatency = statz.NewDistribution2[string, bool]("datasync/uploaded_latency", statz.MetricConfig{
	Description: "The latency of the upload",
	Unit:        units.Milliseconds,
	Labels: []statz.Label{
		{Name: "type", Description: "The data type (file|binary|tabular)."},
		{Name: "status", Description: "If the upload was Successful."},
	},
}, statz.LatencyDistribution)

func main() {
	exporter := perf.NewDevelopmentExporter()
	if err := exporter.Start(); err != nil {
		golog.Global().Panicf("Failed to start: %s", err)
		return
	}
	defer exporter.Stop()

	// Record 100 fake latency values between 0 and 5 seconds.
	for i := 0; i < 25; i++ {
		//nolint:all
		ms := float64(5*time.Second/time.Millisecond) * rand.Float64()
		golog.Global().Infof("Latency %d: %f", i, ms)
		uploadCounter.Inc("binary", true)
		uploadLatency.Observe(ms, "binary", true)
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(64 * time.Second)
}
