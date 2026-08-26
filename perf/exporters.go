// Package perf exposes application performance utilities.
package perf

import (
	"context"
	"os"
	"path"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	mexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	texporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"
	"github.com/caarlos0/env/v11"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.viam.com/utils"
)

const (
	envVarStackDriverProjectID = "STACKDRIVER_PROJECT_ID"

	// ocMetricDomain is the metric type prefix the OpenCensus Stackdriver
	// exporter used for metrics that don't already carry a Google Cloud
	// Monitoring domain. It is preserved here so metric names — and therefore
	// existing dashboards and alerts — survive the move to OpenTelemetry.
	ocMetricDomain = "custom.googleapis.com/opencensus"
)

// gcmDomains are the Cloud Monitoring domains that, when already present in a
// metric name, suppress prefixing with [ocMetricDomain]. Matches the behavior
// of the OpenCensus Stackdriver exporter.
var gcmDomains = []string{"googleapis.com", "kubernetes.io", "istio.io", "knative.dev"}

// Exporter wrapper around Trace and Metric exporter for OpenCensus.
type Exporter interface {
	// Start will start the exporting of metrics and return any errors if failed to start.
	Start() error
	// Stop will stop all exporting and flush remaining metrics.
	Stop()
}

// CloudOptions are options for the production cloud exporter to Google Cloud
// Trace and Cloud Monitoring.
type CloudOptions struct {
	Context      context.Context
	Logger       utils.ZapCompatibleLogger
	MetricPrefix string // Optional metric prefix.
}

// NewCloudExporter creates a new Google Cloud Trace / Cloud Monitoring
// [Exporter] with all options setup and views registered, configured from the
// environment:
//
//	STACKDRIVER_PROJECT_ID       Google Cloud project to export to (defaults to the credentials' project)
//	OCSD_REPORTING_INTERVAL      how often metrics are reported (default 60s)
//	OCSD_BUNDLE_DELAY            how long spans are batched before being exported
//	OCSD_BUNDLE_COUNT_THRESHOLD  maximum number of spans per export
//	OC_SAMPLING_BY_NAME_PER_SEC  root spans sampled per second, per root span name
//	OC_SAMPLING_PROB             probability each root span is sampled
//
// OCSD_WORKERS and OCSD_BUFFER_MAX_BYTES no longer apply; the span queue is
// bounded by span count via the standard OTEL_BSP_MAX_QUEUE_SIZE instead.
func NewCloudExporter(opts CloudOptions) (Exporter, error) {
	envOpts, err := env.ParseAs[struct {
		ReportingInterval    time.Duration `env:"OCSD_REPORTING_INTERVAL"`
		BundleDelayThreshold time.Duration `env:"OCSD_BUNDLE_DELAY"`
		BundleCountThreshold int           `env:"OCSD_BUNDLE_COUNT_THRESHOLD"`
		SamplingByNamePerSec float64       `env:"OC_SAMPLING_BY_NAME_PER_SEC" envDefault:"0"`
		SamplingProbability  float64       `env:"OC_SAMPLING_PROB"            envDefault:"1"`
	}]()
	if err != nil {
		opts.Logger.Errorf("failed to parse cloud exporter options from env, will use defaults: %v", err)
		envOpts.SamplingProbability = 1
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	projectID := os.Getenv(envVarStackDriverProjectID)

	traceOpts := []texporter.Option{
		texporter.WithContext(ctx),
		texporter.WithErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			opts.Logger.Errorw("cloud trace exporter error", "error", err)
		})),
	}
	metricOpts := []mexporter.Option{
		mexporter.WithContext(ctx),
		mexporter.WithMetricDescriptorTypeFormatter(metricTypeFormatter(opts.MetricPrefix)),
		// The OpenCensus exporter attached no labels of its own to metrics
		// (DefaultMonitoringLabels was set to an empty Labels). Keep that
		// behavior: everything identifying is already on the monitored
		// resource, so copying resource attributes onto every metric would
		// only add duplicate labels.
		mexporter.WithFilteredResourceAttributes(mexporter.NoAttributes),
	}
	// Allow a custom Google Cloud project. When unset both exporters fall back
	// to the project of the ambient credentials.
	if projectID != "" {
		traceOpts = append(traceOpts, texporter.WithProjectID(projectID))
		metricOpts = append(metricOpts, mexporter.WithProjectID(projectID))
	}

	res, err := cloudResource(ctx)
	if err != nil {
		return nil, err
	}
	spanExporter, err := texporter.New(traceOpts...)
	if err != nil {
		return nil, err
	}
	metricExporter, err := mexporter.New(metricOpts...)
	if err != nil {
		return nil, err
	}

	return &otelExporter{
		ctx:            ctx,
		logger:         opts.Logger,
		resource:       res,
		sampler:        samplerFromEnvOpts(envOpts.SamplingByNamePerSec, envOpts.SamplingProbability, opts.Logger),
		spanExporter:   spanExporter,
		batchOpts:      batchOptsFromEnvOpts(envOpts.BundleDelayThreshold, envOpts.BundleCountThreshold),
		metricExporter: metricExporter,
		metricInterval: envOpts.ReportingInterval,
	}, nil
}

// cloudResource describes this process to Cloud Trace and Cloud Monitoring.
//
// We report ourselves as a GAE resource even though we're running on Cloud
// Run. GCP only allows for a limited subset of resource types when creating
// custom metrics. The default "Global" is vague, `generic_node` is better but
// doesn't have built in label for version/module. GAE is essentially Cloud Run
// application under the hood and the resource labels with the type match to
// Cloud Run. With a vague resource type we need to add labels on each metric
// which makes the UI in Cloud Monitoring a little hard to reason about the
// labels on the metric vs resource.
//
// See: https://cloud.google.com/monitoring/custom-metrics/creating-metrics#create-metric-desc
func cloudResource(ctx context.Context) (*resource.Resource, error) {
	// For Cloud Run Services applications use
	// See: https://cloud.google.com/run/docs/container-contract#env-vars
	// Cloud Run Worker Pools do NOT have those env variable automatically injected
	// We manually add our own
	module := os.Getenv("K_SERVICE")
	if module == "" {
		// Check fallback env variable for Cloud Run Worker Pool
		module = os.Getenv("GAE_SERVICE")
	}
	if module == "" {
		return serviceResource(""), nil
	}
	version := os.Getenv("K_REVISION")
	if version == "" {
		// Check fallback env variable for Cloud Run Worker Pool
		version = os.Getenv("CRWP_VERSION")
	}

	// Allow for local testing with GCP_COMPUTE_ZONE
	var err error
	zone := os.Getenv("GCP_COMPUTE_ZONE")
	if zone == "" {
		// Get from GCP Metadata
		if zone, err = metadata.ZoneWithContext(ctx); err != nil {
			return nil, err
		}
	}

	// Allow for local testing with GCP_INSTANCE_ID
	instanceID := os.Getenv("GCP_INSTANCE_ID")
	if instanceID == "" {
		// Get from GCP Metadata
		if instanceID, err = metadata.InstanceIDWithContext(ctx); err != nil {
			return nil, err
		}
	}

	// These are the attributes the Google exporters map onto the
	// `gae_instance` monitored resource (module_id, version_id, instance_id,
	// location), which is what the OpenCensus exporter reported by hand.
	return serviceResource(module,
		semconv.CloudProviderGCP,
		semconv.CloudPlatformGCPAppEngine,
		semconv.CloudAvailabilityZone(zone),
		semconv.FaaSName(module),
		semconv.FaaSVersion(version),
		semconv.FaaSInstance(instanceID),
	), nil
}

// metricTypeFormatter reproduces the metric naming of the OpenCensus
// Stackdriver exporter: an optional caller supplied prefix, falling back to
// the OpenCensus custom metric domain for names that don't already carry a
// Cloud Monitoring domain.
func metricTypeFormatter(prefix string) func(metricdata.Metrics) string {
	return func(m metricdata.Metrics) string {
		name := m.Name
		if prefix != "" {
			name = path.Join(prefix, name)
		}
		for _, domain := range gcmDomains {
			if strings.Contains(name, domain) {
				return name
			}
		}
		// Still needed because the name may or may not have a "/" at the beginning.
		return path.Join(ocMetricDomain, name)
	}
}

// batchOptsFromEnvOpts maps the OpenCensus Stackdriver span bundling knobs
// onto the equivalent OpenTelemetry batch span processor settings.
func batchOptsFromEnvOpts(bundleDelay time.Duration, bundleCount int) []sdktrace.BatchSpanProcessorOption {
	var batchOpts []sdktrace.BatchSpanProcessorOption
	if bundleDelay > 0 {
		batchOpts = append(batchOpts, sdktrace.WithBatchTimeout(bundleDelay))
	}
	if bundleCount > 0 {
		batchOpts = append(batchOpts, sdktrace.WithMaxExportBatchSize(bundleCount))
	}
	return batchOpts
}

// samplerFromEnvOpts picks the trace sampler from the OC_SAMPLING_BY_NAME_PER_SEC and
// OC_SAMPLING_PROB env values shared by the cloud and collector exporters.
func samplerFromEnvOpts(byNamePerSec, prob float64, logger utils.ZapCompatibleLogger) sdktrace.Sampler {
	if byNamePerSec != 0 {
		logger.Infow("Using root name rate limiting sampler for tracing",
			"samplePerSec", byNamePerSec)
		return NewRootNameRateLimitingSampler(byNamePerSec)
	}
	if prob != 0 {
		logger.Infow("Using probability sampler for tracing",
			"probability", prob)
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(prob))
	}
	logger.Info("No sampling config found; sampling all traces by default")
	return sdktrace.AlwaysSample()
}
