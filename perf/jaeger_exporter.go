package perf

import (
	"errors"
	"os"

	"contrib.go.opencensus.io/exporter/jaeger"
	"go.opencensus.io/trace"
)

// JaegerExporter exports trace spans directly to a Jaeger collector. It is
// currently only intended for use in local environments.
type JaegerExporter struct {
	e *jaeger.Exporter
}

// Start implements [Exporter].
func (j JaegerExporter) Start() error {
	if err := registerApplicationViews(); err != nil {
		return err
	}
	trace.RegisterExporter(j.e)
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	return nil
}

// Stop implements [Exporter].
func (j JaegerExporter) Stop() {
	trace.UnregisterExporter(j.e)
	j.e.Flush()
}

// JaegerOptions is used to configure [JaegerExporter].
type JaegerOptions struct {
	CollectorEndpoint string
}

var _ Exporter = &JaegerExporter{}

// NewJaegerExporter creates a new Jaeger [Exporter].
func NewJaegerExporter(opts JaegerOptions) (*JaegerExporter, error) {
	if opts.CollectorEndpoint == "" {
		return nil, errors.New("must specify collector endpoint")
	}

	jOpts := jaeger.Options{
		CollectorEndpoint: opts.CollectorEndpoint,
	}

	if serviceName := os.Getenv("SERVICE_NAME"); serviceName != "" {
		jOpts.Process.ServiceName = serviceName
	}
	exp, err := jaeger.NewExporter(jOpts)
	if err != nil {
		return nil, err
	}
	return &JaegerExporter{exp}, nil
}
