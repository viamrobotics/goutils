package perf

import (
	mongowrapper "github.com/opencensus-integrations/gomongowrapper"
)

func registerMongoDBViews() error {
	return mongowrapper.RegisterAllViews()
}
