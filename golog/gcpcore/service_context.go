package gcpcore

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Based on https://github.com/blendle/zapdriver

const serviceContextKey = "serviceContext"

// zapServiceContext adds the correct service information adding the log line
// It is a required field if an error needs to be reported.
//
// see: https://cloud.google.com/error-reporting/reference/rest/v1beta1/zapServiceContext
// see: https://cloud.google.com/error-reporting/docs/formatting-error-messages
func zapServiceContext(sc serviceContext) zap.Field {
	return zap.Object(serviceContextKey, sc)
}

type serviceContext struct {
	Name    string `json:"service"`
	Version string `json:"version"`
}

// MarshalLogObject implements zapcore.ObjectMarshaller interface.
func (sc serviceContext) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("service", sc.Name)
	enc.AddString("version", sc.Version)

	return nil
}

// newServiceContext returns a new service context with name and version.
func newServiceContext(name, version string) *serviceContext {
	return &serviceContext{
		Name:    name,
		Version: version,
	}
}
