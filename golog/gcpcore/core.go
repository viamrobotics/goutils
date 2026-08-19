// Package gcpcore is a custom zap.Core that formats Error logs for GCP Error Reporting
package gcpcore

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Based on https://github.com/blendle/zapdriver

type errorReportingCore struct {
	base           zapcore.Core
	extra          []zapcore.Field
	serviceContext serviceContext
}

// WrapCore returns a zap.Option that wraps the current core with the error
// reporting core. Any ErrorLogs will now be reported in a format that can ingest
// into GCP Error Reporting.
//
// Example:
//
//	core := zap.Must(golog.NewLoggerConfigForGCP().Build(
//			gcpcore.WrapCore(gcpcore.CloudRunServiceAndVersion()),
//		))
//	logger := core.Sugar().Named("web_server")
func WrapCore(name, version string) zap.Option {
	return zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return &errorReportingCore{
			base:           c,
			serviceContext: *newServiceContext(name, version),
		}
	})
}

// CloudRunServiceAndVersion returns a service name and version based on the predefined
// env vars K_SERVICE & K_REVISION. Also works for GCP App Engine.
// See: https://cloud.google.com/run/docs/container-contract
func CloudRunServiceAndVersion() (string, string) {
	return os.Getenv("K_SERVICE"), os.Getenv("K_REVISION")
}

// Enabled tests if the Core is enabled for the level.
func (c *errorReportingCore) Enabled(level zapcore.Level) bool {
	return c.base.Enabled(level)
}

// With returns a new core with the given fields.
func (c *errorReportingCore) With(f []zapcore.Field) zapcore.Core {
	return &errorReportingCore{c, f, c.serviceContext}
}

// Check adds the core logger to the entry if enabled.
func (c *errorReportingCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}

	return ce
}

// Write writes the entry to the underlying core. If an Error level entry is written
// the core will add the fields needed for GCP Error Reporting then write to the
// underlying core.
func (c *errorReportingCore) Write(e zapcore.Entry, f []zapcore.Field) error {
	fields := []zapcore.Field{}
	fields = append(fields, c.extra...)
	fields = append(fields, f...)

	// Only run on Error logs
	if zapcore.ErrorLevel.Enabled(e.Level) {
		fields = c.withSourceLocation(e, fields)
		fields = c.withServiceContext(fields)
		fields = c.withErrorReport(e, fields)
	}

	return c.base.Write(e, fields)
}

// Sync calls the underly cores Sync.
func (c *errorReportingCore) Sync() error {
	return c.base.Sync()
}

func (c *errorReportingCore) withSourceLocation(ent zapcore.Entry, fields []zapcore.Field) []zapcore.Field {
	// If the source location was manually set, don't overwrite it
	for i := range fields {
		if fields[i].Key == sourceKey {
			return fields
		}
	}

	if !ent.Caller.Defined {
		return fields
	}

	return append(fields, SourceLocation(ent.Caller.PC, ent.Caller.File, ent.Caller.Line, true))
}

func (c *errorReportingCore) withServiceContext(fields []zapcore.Field) []zapcore.Field {
	// If the service context was manually set, don't overwrite it
	for i := range fields {
		if fields[i].Key == serviceContextKey {
			return fields
		}
	}

	return append(fields, zapServiceContext(c.serviceContext))
}

func (c *errorReportingCore) withErrorReport(ent zapcore.Entry, fields []zapcore.Field) []zapcore.Field {
	// If the error report was manually set, don't overwrite it
	for i := range fields {
		if fields[i].Key == contextKey {
			return fields
		}
	}

	if !ent.Caller.Defined {
		return fields
	}

	return append(fields, zapErrorReport(ent.Caller.PC, ent.Caller.File, ent.Caller.Line, true))
}
