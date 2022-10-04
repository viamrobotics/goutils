package perf

import (
	"go.uber.org/multierr"
)

// registerApplicationViews registers all the default views we may need for the application. gRPC, MongoDB, HTTP, etc...
func registerApplicationViews() error {
	return multierr.Combine(
		registerGrpcViews(),
		registerHTTPViews(),
	)
}
