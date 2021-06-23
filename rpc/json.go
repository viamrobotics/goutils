// Package rpc provides a remote procedure call (RPC) library based on gRPC.
//
// In a server context, this package should be preferred over gRPC directly
// since it provides higher level configuration with more features built in,
// such as grpc-web, gRPC via RESTful JSON, and gRPC via WebRTC.
//
// Note: Authentication/Authorization/Encryption are not yet supported concepts.
// It is assumed this will be used in a trusted, secure environment.
package rpc

import (
	"bytes"
	"context"
	"reflect"

	"github.com/go-errors/errors"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/runtime/protoiface"
)

// JSONPB are the JSON protobuf options we use globally.
var JSONPB = &runtime.JSONPb{
	MarshalOptions: protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	},
}

var (
	contextT = reflect.TypeOf((*context.Context)(nil)).Elem()
	messageT = reflect.TypeOf((*protoiface.MessageV1)(nil)).Elem()
)

// CallClientMethodLineJSON calls a method on the given client by deserializing data
// expected to be from a line format of JSON where the format is:
// <MethodName> [JSON]
func CallClientMethodLineJSON(ctx context.Context, client interface{}, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	dataSplit := bytes.SplitN(data, []byte(" "), 2)
	methodName := string(dataSplit[0])
	clientV := reflect.ValueOf(client)
	clientT := clientV.Type()
	method, ok := clientT.MethodByName(methodName)
	if !ok {
		return nil, errors.Errorf("method %q does not exist", methodName)
	}
	if method.Type.NumIn() != 4 { // (Client, context.Context, pb.<Method>Request, ...grpc.CallOption)
		return nil, errors.Errorf("method %q does not look unary", methodName)
	}
	if method.Type.In(1) != contextT {
		return nil, errors.Errorf("expected method %q first param to be context", methodName)
	}
	if !method.Type.In(2).Implements(messageT) {
		return nil, errors.Errorf("expected method %q second param to be a proto message", methodName)
	}
	message := reflect.New(method.Type.In(2).Elem()).Interface()
	if len(dataSplit) > 1 && len(dataSplit[1]) > 0 {
		if err := JSONPB.Unmarshal(dataSplit[1], message); err != nil {
			return nil, errors.Errorf("error unmarshaling into message: %w", err)
		}
	}
	// ignore opts
	rets := clientV.MethodByName(methodName).Call([]reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(message),
	})
	if errV := rets[1]; errV.IsValid() && !errV.IsZero() {
		gErr := status.Convert(errV.Interface().(error)).Message()
		return nil, errors.Errorf("error calling method %q: %s", methodName, gErr)
	}
	resp := rets[0].Interface()
	md, err := JSONPB.Marshal(resp)
	if err != nil {
		return nil, errors.Errorf("error marshaling response: %w", err)
	}
	return md, nil
}
