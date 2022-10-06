// Package protojson provides helpers to marshal proto.Message to json
package protojson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	protoMessageType = reflect.TypeOf((*proto.Message)(nil)).Elem()

	defaultMarshaler = Marshaler{DefaultMarshalingOptions()}
)

// Marshaler used to marshal data to json repecting proto.Message types.
type Marshaler struct {
	Opts MarshalingOptions
}

// Marshal encodes the interface to json. It identifies if a proto.Message or an slice/array of
// proto.Message are passed in and for those types uses the protobuf protojson marshaler. Otherwise
// uses the standard json.Marshal().
//
// Note beware of its limitations:
//   - Does not support proto.Message within a map.
//   - Does not support proto.Message embedded within another struct.
func (m *Marshaler) Marshal(data interface{}) ([]byte, error) {
	protoMarshaler := m.Opts.JSONOptions

	// If protojson enabled and type is proto.Message encode with protojson
	if m.Opts.EnableProtoJSON && isProtoMessage(data) {
		return protoMarshaler.Marshal(data.(proto.Message))
	} else if m.Opts.EnableProtoJSON && isSliceOfProtoMessage(data) {
		return marshalSliceOfProtos(protoMarshaler, data)
	}

	// fallback to default json marshaler
	return json.Marshal(data)
}

// MarshalToInterface encodes the interface to generic json map interface. It identifies if a proto.Message or an slice/array of
// proto.Message are passed in and for those types uses the protobuf protojson marshaler. Otherwise
// uses the standard json.Marshal().
//
// Note beware of its limitations:
//   - Does not support proto.Message within a map.
//   - Does not support proto.Message embedded within another struct.
func (m *Marshaler) MarshalToInterface(data interface{}) (interface{}, error) {
	out, err := m.Marshal(data)
	if err != nil {
		return nil, err
	}

	var x interface{}
	err = json.Unmarshal(out, &x)
	if err != nil {
		return nil, err
	}

	return x, nil
}

// Marshal encodes the interface to json. It identifies if a proto.Message or an slice/array of
// proto.Message are passed in and for those types uses the protobuf protojson marshaler. Otherwise
// uses the standard json.Marshal().
//
// Note beware of its limitations:
//   - Does not support proto.Message within a map.
//   - Does not support proto.Message embedded within another struct.
func Marshal(data interface{}) ([]byte, error) {
	return defaultMarshaler.Marshal(data)
}

// MarshalToInterface encodes the interface to generic json map interface. It identifies if a proto.Message or an slice/array of
// proto.Message are passed in and for those types uses the protobuf protojson marshaler. Otherwise
// uses the standard json.Marshal().
//
// Note beware of its limitations:
//   - Does not support proto.Message within a map.
//   - Does not support proto.Message embedded within another struct.
func MarshalToInterface(data interface{}) (interface{}, error) {
	return defaultMarshaler.MarshalToInterface(data)
}

func isSliceOfProtoMessage(input interface{}) bool {
	t := reflect.TypeOf(input)
	if t.Kind() == reflect.Array || t.Kind() == reflect.Slice {
		return t.Elem().Implements(protoMessageType)
	}
	return false
}

func isProtoMessage(input interface{}) bool {
	_, ok := input.(proto.Message)
	return ok
}

// A bit of a hack to encode an slice of protos. The protojson implementation only supports
// a single proto.Message being encoded. This loops through a input array using reflection and
// encodes each element to json using the protojson marshaler and contructs the array.
func marshalSliceOfProtos(marshaler protojson.MarshalOptions, input interface{}) ([]byte, error) {
	inputType := reflect.ValueOf(input)

	if inputType.Len() == 0 {
		return []byte("[]"), nil
	}

	var b bytes.Buffer
	x := bufio.NewWriter(&b)

	_, err := x.WriteString("[")
	if err != nil {
		return nil, err
	}

	for i := 0; i < inputType.Len(); i++ {
		protoItem := inputType.Index(i).Interface().(proto.Message)
		jsItem, err := marshaler.Marshal(protoItem)
		if err != nil {
			return nil, err
		}

		_, err = x.WriteString(string(jsItem))
		if err != nil {
			return nil, err
		}

		if i != inputType.Len()-1 {
			_, err = x.WriteString(",")
			if err != nil {
				return nil, err
			}
		}
	}

	_, err = x.WriteString("]")
	if err != nil {
		return nil, err
	}

	err = x.Flush()
	if err != nil {
		return nil, err
	}

	js := b.Bytes()
	return js, nil
}
