package perf

import (
	"errors"
	"time"

	"contrib.go.opencensus.io/exporter/ocagent"
	"github.com/caarlos0/env/v11"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"

	"go.viam.com/utils"
)

// OCAgentExporter exports traces and metrics over gRPC to an OpenCensus
// agent/collector. The OpenTelemetry Collector accepts the same protocol via
// its "opencensus" receiver.
type OCAgentExporter struct {
	e       *ocagent.Exporter
	sampler trace.Sampler
}

var _ Exporter = &OCAgentExporter{}

// OCAgentOptions is used to configure [OCAgentExporter].
type OCAgentOptions struct {
	Logger utils.ZapCompatibleLogger
	// Address is the "host:port" of the collector. If empty, OC_AGENT_ADDRESS
	// is used instead.
	Address string
}

// NewOCAgentExporter creates a new OpenCensus agent/collector [Exporter],
// configured from the environment:
//
//	OC_AGENT_ADDRESS             collector "host:port" (used when OCAgentOptions.Address is empty)
//	OC_AGENT_INSECURE            dial without TLS (default true)
//	OC_AGENT_RECONNECTION_PERIOD retry interval for reconnecting to the collector
//	OC_SAMPLING_BY_NAME_PER_SEC  same sampling behavior as the cloud exporter
//	OC_SAMPLING_PROB             same sampling behavior as the cloud exporter
//	SERVICE_NAME                 service name reported with spans
func NewOCAgentExporter(opts OCAgentOptions) (*OCAgentExporter, error) {
	envOpts, err := env.ParseAs[struct {
		Address              string        `env:"OC_AGENT_ADDRESS"`
		Insecure             bool          `env:"OC_AGENT_INSECURE"            envDefault:"true"`
		ReconnectionPeriod   time.Duration `env:"OC_AGENT_RECONNECTION_PERIOD"`
		SamplingByNamePerSec float64       `env:"OC_SAMPLING_BY_NAME_PER_SEC"  envDefault:"0"`
		SamplingProbability  float64       `env:"OC_SAMPLING_PROB"             envDefault:"1"`
		ServiceName          string        `env:"SERVICE_NAME"`
	}]()
	if err != nil {
		opts.Logger.Errorf("failed to parse opencensus agent exporter options from env, will use defaults: %v", err)
		envOpts.Insecure = true
		envOpts.SamplingProbability = 1
	}

	address := opts.Address
	if address == "" {
		address = envOpts.Address
	}
	if address == "" {
		return nil, errors.New("must specify collector address via OCAgentOptions.Address or OC_AGENT_ADDRESS")
	}

	ocOpts := []ocagent.ExporterOption{ocagent.WithAddress(address)}
	if envOpts.Insecure {
		ocOpts = append(ocOpts, ocagent.WithInsecure())
	}
	if envOpts.ReconnectionPeriod > 0 {
		ocOpts = append(ocOpts, ocagent.WithReconnectionPeriod(envOpts.ReconnectionPeriod))
	}
	if envOpts.ServiceName != "" {
		ocOpts = append(ocOpts, ocagent.WithServiceName(envOpts.ServiceName))
	}

	exp, err := ocagent.NewUnstartedExporter(ocOpts...)
	if err != nil {
		return nil, err
	}
	return &OCAgentExporter{
		e:       exp,
		sampler: samplerFromEnvOpts(envOpts.SamplingByNamePerSec, envOpts.SamplingProbability, opts.Logger),
	}, nil
}

// Start implements [Exporter]. Registers views and starts trace/metric
// exporting to the collector.
func (o *OCAgentExporter) Start() error {
	if err := registerApplicationViews(); err != nil {
		return err
	}
	if err := o.e.Start(); err != nil {
		return err
	}
	view.RegisterExporter(o.e)
	trace.RegisterExporter(o.e)
	trace.ApplyConfig(trace.Config{DefaultSampler: o.sampler})
	return nil
}

// Stop implements [Exporter]. Flushes any pending spans/metrics and closes the
// connection to the collector.
func (o *OCAgentExporter) Stop() {
	trace.UnregisterExporter(o.e)
	view.UnregisterExporter(o.e)
	o.e.Flush()
	utils.UncheckedError(o.e.Stop())
}
