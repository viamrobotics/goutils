package perf

import (
	"errors"
	"os"
	"strings"

	"github.com/edaniels/golog"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.viam.com/utils"
)

// JaegerExporter exports trace spans over OTLP to a Jaeger collector. It is
// currently only intended for use in local environments.
type JaegerExporter struct {
	otelExporter
}

var _ Exporter = &JaegerExporter{}

// JaegerOptions is used to configure [JaegerExporter].
type JaegerOptions struct {
	// CollectorEndpoint is Jaeger's OTLP/HTTP endpoint, e.g.
	// "http://localhost:4318". A bare "host:port" is also accepted and is
	// dialed without TLS.
	CollectorEndpoint string
	// Logger is optional and defaults to the global golog logger.
	Logger utils.ZapCompatibleLogger
}

// NewJaegerExporter creates a new Jaeger [Exporter]. Jaeger has accepted OTLP
// natively since v1.35, so spans are sent over OTLP rather than Jaeger's own
// (since removed) collector protocol.
func NewJaegerExporter(opts JaegerOptions) (*JaegerExporter, error) {
	if opts.CollectorEndpoint == "" {
		return nil, errors.New("must specify collector endpoint")
	}

	var otlpOpts []otlptracehttp.Option
	if strings.Contains(opts.CollectorEndpoint, "://") {
		otlpOpts = append(otlpOpts, otlptracehttp.WithEndpointURL(opts.CollectorEndpoint))
	} else {
		otlpOpts = append(otlpOpts,
			otlptracehttp.WithEndpoint(opts.CollectorEndpoint),
			otlptracehttp.WithInsecure(),
		)
	}

	logger := opts.Logger
	if logger == nil {
		logger = golog.Global()
	}

	return &JaegerExporter{otelExporter{
		logger:       logger,
		resource:     serviceResource(os.Getenv("SERVICE_NAME")),
		sampler:      sdktrace.AlwaysSample(),
		spanExporter: otlptracehttp.NewUnstarted(otlpOpts...),
	}}, nil
}
