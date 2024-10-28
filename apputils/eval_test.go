package apputils

import (
	"testing"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.viam.com/test"
)

func TestIsMap(t *testing.T) {
	for _, tc := range []struct {
		description string
		value       any
		expected    bool
	}{
		{
			description: "map",
			value:       map[string]interface{}{"this": "is_a_map"},
			expected:    true,
		},
		{
			description: "bson.M",
			value:       bson.M{"this": "is_a_map"},
			expected:    true,
		},
		{
			description: "struct",
			value:       struct{}{},
			expected:    false,
		},
		{
			description: "nil",
			value:       nil,
			expected:    false,
		},
		{
			description: "bson.A",
			value:       bson.A{"is_not_a_map"},
			expected:    false,
		},
		{
			description: "bson.D",
			value:       bson.D{{"this", "is_not_a_map"}},
			expected:    false,
		},
		{
			description: "bson.E",
			value:       bson.E{"this", "is_not_a_map"},
			expected:    false,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			test.That(t, IsMap(tc.value), test.ShouldEqual, tc.expected)
		})
	}
}

func TestIsSlice(t *testing.T) {
	for _, tc := range []struct {
		description string
		value       any
		expected    bool
	}{
		{
			description: "bson.A",
			value:       bson.A{"is_a_slice"},
			expected:    true,
		},
		{
			description: "[]any",
			value:       []any{},
			expected:    true,
		},
		{
			description: "map",
			value:       map[string]interface{}{"this": "is_not_a_slice"},
			expected:    false,
		},
		{
			description: "bson.M",
			value:       bson.M{"this": "is_not_a_slice"},
			expected:    false,
		},
		{
			description: "struct",
			value:       struct{}{},
			expected:    false,
		},
		{
			description: "integer",
			value:       5,
			expected:    false,
		},
		{
			description: "string",
			value:       "string",
			expected:    false,
		},
		{
			description: "bool",
			value:       false,
			expected:    false,
		},
		{
			description: "nil",
			value:       nil,
			expected:    false,
		},
		{
			description: "bson.D",
			value:       bson.D{{"this", "is_a_slice"}},
			expected:    true,
		},
		{
			description: "bson.E",
			value:       bson.E{"this", "is_not_a_slice"},
			expected:    false,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			test.That(t, IsSlice(tc.value), test.ShouldEqual, tc.expected)
		})
	}
}

func TestConvertToNumericSlice(t *testing.T) {
	for _, tc := range []struct {
		description string
		value       any
		expectedRes []float64
		expectedErr error
	}{
		{
			description: "non-slice",
			value:       5,
			expectedRes: nil,
			expectedErr: errors.New("input is not a slice"),
		},
		{
			description: "non-typed slice containing non-numerical element",
			value:       []any{"this element is not a slice"},
			expectedRes: nil,
			expectedErr: errors.New("element at index 0 is not a numeric type"),
		},
		{
			description: "typed non-numerical slice",
			value:       []string{"hello"},
			expectedRes: nil,
			expectedErr: errors.New("element at index 0 is not a numeric type"),
		},
		{
			description: "non-typed slice containing numerical and non-numerical elements",
			value:       []any{34, "this element is not a slice"},
			expectedRes: nil,
			expectedErr: errors.New("element at index 1 is not a numeric type"),
		},
		{
			description: "non-typed slice containing numerical elements (int values)",
			value:       []any{34, 67},
			expectedRes: []float64{34, 67},
			expectedErr: nil,
		},
		{
			description: "non-typed slice containing numerical elements (float64 values)",
			value:       []any{34.5, 67.8},
			expectedRes: []float64{34.5, 67.8},
			expectedErr: nil,
		},
		{
			description: "non-typed slice containing numerical elements (int and float64 values)",
			value:       []any{34, 67.8},
			expectedRes: []float64{34, 67.8},
			expectedErr: nil,
		},
		{
			description: "typed int slice",
			value:       []int{34, 67},
			expectedRes: []float64{34, 67},
			expectedErr: nil,
		},
		{
			description: "non-typed slice containing numerical elements (with zero, negative int, negative float64)",
			value:       []any{-5, 0, -7.2, 17, 0.2},
			expectedRes: []float64{-5, 0, -7.2, 17, 0.2},
			expectedErr: nil,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			res, err := ConvertToFloat64Slice(tc.value)
			test.That(t, res, test.ShouldResemble, tc.expectedRes)
			if tc.expectedErr != nil {
				test.That(t, err.Error(), test.ShouldContainSubstring, tc.expectedErr.Error())
			} else {
				test.That(t, err, test.ShouldBeNil)
			}
		})
	}
}
