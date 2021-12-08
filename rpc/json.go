// Package rpc provides a remote procedure call (RPC) library based on gRPC.
//
// In a server context, this package should be preferred over gRPC directly
// since it provides higher level configuration with more features built in,
// such as grpc-web, gRPC via RESTful JSON, and gRPC via WebRTC.
//
// WebRTC services gRPC over DataChannels. The work was initially adapted from
// https://github.com/jsmouret/grpc-over-webrtc.
package rpc

import (
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/encoding/protojson"
)

// JSONPB are the JSON protobuf options we use globally.
var JSONPB = &runtime.JSONPb{
	MarshalOptions: protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	},
}
