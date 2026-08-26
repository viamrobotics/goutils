package perf

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"

	"go.viam.com/utils"
)

// CollectorExporter exports traces and metrics over OTLP/gRPC to an
// OpenTelemetry Collector.
type CollectorExporter struct {
	otelExporter
}

var _ Exporter = &CollectorExporter{}

// CollectorOptions is used to configure [CollectorExporter].
type CollectorOptions struct {
	Logger utils.ZapCompatibleLogger
	// Address is the "host:port" of the collector's OTLP gRPC receiver
	// (typically port 4317). It may also be given as a full URL, in which case
	// the scheme decides whether TLS is used. If empty, OC_AGENT_ADDRESS is
	// used instead.
	Address string
}

// NewCollectorExporter creates a new OpenTelemetry Collector [Exporter],
// configured from the environment:
//
//	OC_AGENT_ADDRESS             collector "host:port" (used when CollectorOptions.Address is empty)
//	OC_AGENT_INSECURE            dial without TLS (default true)
//	OC_AGENT_RECONNECTION_PERIOD retry interval for reconnecting to the collector
//	OC_SAMPLING_BY_NAME_PER_SEC  same sampling behavior as the cloud exporter
//	OC_SAMPLING_PROB             same sampling behavior as the cloud exporter
//	SERVICE_NAME                 service name reported with spans and metrics
//
// Note that these are the same variable names the OpenCensus agent exporter
// this replaces used; the address now has to point at the collector's OTLP
// receiver rather than its OpenCensus receiver.
func NewCollectorExporter(opts CollectorOptions) (*CollectorExporter, error) {
	envOpts, err := env.ParseAs[struct {
		Address              string        `env:"OC_AGENT_ADDRESS"`
		Insecure             bool          `env:"OC_AGENT_INSECURE"            envDefault:"true"`
		ReconnectionPeriod   time.Duration `env:"OC_AGENT_RECONNECTION_PERIOD"`
		SamplingByNamePerSec float64       `env:"OC_SAMPLING_BY_NAME_PER_SEC"  envDefault:"0"`
		SamplingProbability  float64       `env:"OC_SAMPLING_PROB"             envDefault:"1"`
		KService             string        `env:"K_SERVICE"`
		GAEService           string        `env:"GAE_SERVICE"`
		ServiceName          string        `env:"SERVICE_NAME"`
	}]()
	if err != nil {
		opts.Logger.Errorf("failed to parse collector exporter options from env, will use defaults: %v", err)
		envOpts.Insecure = true
		envOpts.SamplingProbability = 1
	}

	address := opts.Address
	if address == "" {
		address = envOpts.Address
	}
	if address == "" {
		return nil, errors.New("must specify collector address via CollectorOptions.Address or OC_AGENT_ADDRESS")
	}

	traceOpts := []otlptracegrpc.Option{}
	metricOpts := []otlpmetricgrpc.Option{}
	if strings.Contains(address, "://") {
		traceOpts = append(traceOpts, otlptracegrpc.WithEndpointURL(address))
		metricOpts = append(metricOpts, otlpmetricgrpc.WithEndpointURL(address))
	} else {
		traceOpts = append(traceOpts, otlptracegrpc.WithEndpoint(address))
		metricOpts = append(metricOpts, otlpmetricgrpc.WithEndpoint(address))
		if envOpts.Insecure {
			traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
			metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
		}
	}
	if envOpts.ReconnectionPeriod > 0 {
		traceOpts = append(traceOpts, otlptracegrpc.WithReconnectionPeriod(envOpts.ReconnectionPeriod))
		metricOpts = append(metricOpts, otlpmetricgrpc.WithReconnectionPeriod(envOpts.ReconnectionPeriod))
	}

	var serviceName string
	for _, candidate := range []string{envOpts.KService, envOpts.GAEService, envOpts.ServiceName} {
		if candidate != "" {
			serviceName = candidate
			break
		}
	}

	// Both exporters connect lazily, so this only fails on invalid options.
	spanExporter := otlptracegrpc.NewUnstarted(traceOpts...)
	metricExporter, err := otlpmetricgrpc.New(context.Background(), metricOpts...)
	if err != nil {
		return nil, err
	}

	return &CollectorExporter{otelExporter{
		logger:         opts.Logger,
		resource:       serviceResource(serviceName),
		sampler:        samplerFromEnvOpts(envOpts.SamplingByNamePerSec, envOpts.SamplingProbability, opts.Logger),
		spanExporter:   spanExporter,
		metricExporter: metricExporter,
	}}, nil
}
