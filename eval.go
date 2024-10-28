package utils

import (
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"reflect"
	"regexp"
)

// Eval is a typed representation of logic that can be evaluated.
type Eval struct {
	Operator EvalOperator `bson:"operator" json:"operator" mapstructure:"operator"`
	Value    any          `bson:"value"    json:"value"    mapstructure:"value"`
}

// EvalOperator is a typed representation of an operator used to describe a condition for a trigger event.
type EvalOperator string

var (
	// LessThan is a typed representation of the < operator.
	LessThan EvalOperator = "lt"
	// LessThanOrEqual is a typed representation of the <= operator.
	LessThanOrEqual EvalOperator = "lte"
	// GreaterThan is a typed representation of the > operator.
	GreaterThan EvalOperator = "gt"
	// GreaterThanOrEqual is a typed representation of the >= operator.
	GreaterThanOrEqual EvalOperator = "gte"
	// Equal is a typed representation of the == operator.
	Equal EvalOperator = "eq"
	// NotEqual is a typed representation of the != operator.
	NotEqual EvalOperator = "neq"
	// Regex is a typed representation of the regular expression operator.
	Regex EvalOperator = "regex"
)

// ToReadableString returns a string representation of an evaluable operator.
func (operator EvalOperator) ToReadableString() string {
	switch operator {
	case LessThan:
		return "less than"
	case LessThanOrEqual:
		return "less than or equal to"
	case GreaterThan:
		return "greater than"
	case GreaterThanOrEqual:
		return "greater than or equal to"
	case Equal:
		return "equal to"
	case NotEqual:
		return "not equal to"
	case Regex:
		return "a match on the regular expression"
	}
	return ""
}

// Evaluate returns whether 'readingValue' is [insert operator here] 'conditionValue'.
func (operator EvalOperator) Evaluate(readingValue, conditionValue any) (bool, error) {
	if IsMap(readingValue) && IsMap(conditionValue) {
		mapReadingValue, ok := readingValue.(bson.M)
		if !ok {
			return false, errors.New("cast assertion failed: reading must be bson.M type")
		}
		mapConditionValue, ok := conditionValue.(map[string]interface{})
		if !ok {
			return false, errors.New("cast assertion failed: condition must be map[string]interface{} type")
		}
		return operator.evaluateMaps(mapReadingValue, mapConditionValue)
	}
	if IsNumber(readingValue) && IsNumber(conditionValue) {
		readingValueFloat, err := ConvertToFloat64(readingValue)
		if err != nil {
			return false, err
		}
		conditionValueFloat, err := ConvertToFloat64(conditionValue)
		if err != nil {
			return false, err
		}
		return operator.evaluateFloats(readingValueFloat, conditionValueFloat)
	}
	if IsSlice(readingValue) && IsSlice(conditionValue) {
		// Compare slices of numerical values (so far is the only needed use case, but extend here if
		// support for non-numerical slice comparing is needed).
		readingValueSlice, err := ConvertToFloat64Slice(readingValue)
		if err != nil {
			return false, err
		}
		conditionValueSlice, err := ConvertToFloat64Slice(conditionValue)
		if err != nil {
			return false, err
		}
		return evaluateLists[float64](operator, readingValueSlice, conditionValueSlice)
	}
	return operator.evaluateValues(readingValue, conditionValue)
}

// IsMap returns whether value is a map type.
func IsMap(value interface{}) bool {
	return value != nil && (reflect.TypeOf(value).Kind() == reflect.Map)
}

// IsSlice returns whether value is a list of any type.
func IsSlice(value interface{}) bool {
	return value != nil && (reflect.TypeOf(value).Kind() == reflect.Slice)
}

// IsNumber takes any value and returns whether its a numeric type.
func IsNumber(value interface{}) bool {
	v := reflect.ValueOf(value)

	switch v.Kind() { //nolint:exhaustive
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// IsInList returns whether item is in list.
func IsInList[T any](list []T, item T) bool {
	for _, listItem := range list {
		if reflect.DeepEqual(item, listItem) {
			return true
		}
	}
	return false
}

// IsMatchForRegex first validates 'x' and 'y' are strings and then returns whether 'y' is a match on regex 'x'.
func IsMatchForRegex(x, y any) (bool, error) {
	xString, ok := (x).(string)
	if !ok {
		return false, errors.Errorf("type assertion failed in eval operator: %v is not a string", x)
	}
	yString, ok := (y).(string)
	if !ok {
		return false, errors.Errorf("type assertion failed in eval operator: %v is not a string", y)
	}
	r, err := regexp.Compile(yString)
	if err != nil {
		return false, err
	}
	return r.MatchString(xString), nil
}

// ConvertToFloat64 takes any value and returns the converted float64 value if it can be converted.
func ConvertToFloat64(val interface{}) (float64, error) {
	v := reflect.ValueOf(val)
	valType := v.Kind()

	if valType == reflect.Int || valType == reflect.Int8 || valType == reflect.Int16 ||
		valType == reflect.Int32 || valType == reflect.Int64 {
		// check if the int value converted are the sames
		return float64(v.Int()), nil
	}

	if valType == reflect.Uint || valType == reflect.Uint8 || valType == reflect.Uint16 ||
		valType == reflect.Uint32 || valType == reflect.Uint64 {
		return float64(v.Uint()), nil
	}

	if valType == reflect.Float32 || valType == reflect.Float64 {
		return v.Float(), nil
	}

	return -1, errors.Errorf("%v cannot be type casted to float64", val)
}

// ConvertToFloat64Slice attempts to convert an `any` to a slice of float64 if possible.
func ConvertToFloat64Slice(input any) ([]float64, error) {
	// Use reflection to check if the input is a slice
	val := reflect.ValueOf(input)
	if val.Kind() != reflect.Slice {
		return nil, errors.New("input is not a slice")
	}

	// Prepare a float64 slice to hold the numerical values
	float64Slice := make([]float64, 0, val.Len())

	// Iterate over the elements of the slice
	for i := 0; i < val.Len(); i++ {
		element := val.Index(i).Interface()
		val, err := ConvertToFloat64(element)
		if err != nil {
			return nil, errors.WithMessagef(err, "element at index %d is not a numeric type", i)
		}
		float64Slice = append(float64Slice, val)
	}

	return float64Slice, nil
}

// evaluateLists returns whether 'readingValue' is [insert operator here] 'conditionValue'.
func evaluateLists[T any](operator EvalOperator, readingValue, conditionValue any) (bool, error) {
	ret, op := true, operator

	// Special case: if operator is NotEqual, evaluate recursively as Equal with a negated return value.
	if operator == NotEqual {
		ret, op = !ret, Equal
	}

	readingValueSlice, ok := readingValue.([]T)
	if !ok {
		return !ret, errors.Errorf("cannot convert the following reading value %v into a slice", readingValue)
	}
	conditionValueSlice, ok := conditionValue.([]T)
	if !ok {
		return !ret, errors.Errorf("cannot convert the following condition value %v into a slice", conditionValue)
	}
	if len(readingValueSlice) != len(conditionValueSlice) {
		return !ret, nil
	}
	for index, conditionValueSliceAtIndex := range conditionValueSlice {
		if satisfied, err := op.Evaluate(readingValueSlice[index], conditionValueSliceAtIndex); !satisfied || err != nil {
			return !ret, err
		}
	}
	return ret, nil
}

// IsValidOperator returns whether the operator is in the list of provided valid operators.
func (operator EvalOperator) IsValidOperator(validOperators ...EvalOperator) bool {
	return IsInList(validOperators, operator)
}

// evaluateFloats returns whether 'floatReadingValue' is [insert operator here] 'floatConditionValue'.
func (operator EvalOperator) evaluateFloats(floatReadingValue, floatConditionValue float64) (bool, error) {
	switch operator {
	case GreaterThan:
		return (floatReadingValue > floatConditionValue), nil
	case GreaterThanOrEqual:
		return (floatReadingValue >= floatConditionValue), nil
	case LessThan:
		return (floatReadingValue < floatConditionValue), nil
	case LessThanOrEqual:
		return (floatReadingValue <= floatConditionValue), nil
	case Equal:
		return (floatReadingValue == floatConditionValue), nil
	case NotEqual:
		return (floatReadingValue != floatConditionValue), nil
	case Regex:
		fallthrough
	default:
	}
	return false, errors.Errorf("cannot use the following operator: %s to compare %v against %v", operator.ToReadableString(),
		floatReadingValue, floatConditionValue)
}

// evaluateValues returns whether 'readingValue' is [insert operator here] 'conditionValue'.
func (operator EvalOperator) evaluateValues(readingValue, conditionValue any) (bool, error) {
	switch operator {
	case Equal:
		return reflect.DeepEqual(readingValue, conditionValue), nil
	case NotEqual:
		return !reflect.DeepEqual(readingValue, conditionValue), nil
	case Regex:
		return IsMatchForRegex(readingValue, conditionValue)
	case LessThan, LessThanOrEqual, GreaterThan, GreaterThanOrEqual:
		fallthrough
	default:
	}
	return false, errors.Errorf("cannot use the following operator: %s to compare %v against %v", operator.ToReadableString(),
		readingValue, conditionValue)
}

// evaluateMaps returns true if each value in mapReadingValue satisfies the operator against its associated value in mapConditionValue.
func (operator EvalOperator) evaluateMaps(mapReadingValue bson.M, mapConditionValue map[string]interface{}) (bool, error) {
	ret, op := true, operator

	// Special case: if operator is NotEqual, evaluate recursively as Equal with a negated return value.
	if operator == NotEqual {
		ret, op = !ret, Equal
	}

	for conditionKey, conditionKeyValue := range mapConditionValue {
		readingKeyValue, ok := mapReadingValue[conditionKey]
		if !ok {
			// (TODO APP-5425) Consider logging if trigger will never be satisfied
			return !ret, nil
		}
		res, err := op.Evaluate(readingKeyValue, conditionKeyValue)
		if !res || err != nil {
			return !ret, err
		}
	}
	return ret, nil
}
