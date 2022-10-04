package protojson

import "google.golang.org/protobuf/encoding/protojson"

// JSONOptions is the underlying protojson marshal options.
type JSONOptions = protojson.MarshalOptions

// MarshalingOptions has options for how web http middleware marshals data to the clients.
type MarshalingOptions struct {
	// Enable protojson encoding in API responses and any json encoding.
	EnableProtoJSON bool

	// Options used for protojson marshaling
	JSONOptions
}

// DefaultMarshalingOptions returns a default set of options. It has protojson disabled by default.
func DefaultMarshalingOptions() MarshalingOptions {
	return MarshalingOptions{
		// Disabled by default.
		EnableProtoJSON: false,

		JSONOptions: JSONOptions{
			UseProtoNames: true,
			AllowPartial:  true,
		},
	}
}
