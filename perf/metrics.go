package perf

import (
	"fmt"
	"log"

	"go.opencensus.io/stats/view"
	"go.uber.org/multierr"
)

// registerApplicationViews registers all the default views we may need for the application. gRPC, MongoDB, HTTP, etc...
func registerApplicationViews() error {
	return multierr.Combine(
		registerGrpcViews(),
		registerHTTPViews(),
	)
}

// indent these many spaces.
const indent = "  "

// newPrintExporter creates a new metric exporter that prints to the console.
// This should NOT be used for production workloads.
func newPrintExporter() view.Exporter {
	return &printExporter{}
}

// printExporter is a stats and trace exporter that logs
// the exported data to the console.
//
// The intent is help new users familiarize themselves with the
// capabilities of opencensus.
//
// This should NOT be used for production workloads.
type printExporter struct{}

// ExportView prints the metric data to the console.
func (e *printExporter) ExportView(vd *view.Data) {
	for _, row := range vd.Rows {
		log.Printf("%v %-45s", vd.End.Format("15:04:05"), vd.View.Name)

		var info string
		switch v := row.Data.(type) {
		case *view.DistributionData:
			info = fmt.Sprintf("distribution: min=%.1f max=%.1f mean=%.1f", v.Min, v.Max, v.Mean)
		case *view.CountData:
			info = fmt.Sprintf("count:        value=%v", v.Value)
		case *view.SumData:
			info = fmt.Sprintf("sum:          value=%v", v.Value)
		case *view.LastValueData:
			info = fmt.Sprintf("last:         value=%v", v.Value)
		}
		log.Println(info)

		for _, tag := range row.Tags {
			log.Printf("%v- %v=%v\n", indent, tag.Key.Name(), tag.Value)
		}
	}
}
