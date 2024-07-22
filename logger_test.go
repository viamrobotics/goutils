package utils

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/edaniels/golog"
	"go.uber.org/zap"
	"go.viam.com/test"
)

// InvalidLogger fulfills the ZapCompatibleLogger interface without a Named() or Sublogger() method, used to test
// that utils.Sublogger() should fail without either of these methods.
type InvalidLogger struct {
	name string
}

func (m *InvalidLogger) Desugar() *zap.Logger {
	return zap.NewNop()
}

func (m *InvalidLogger) With(args ...interface{}) *zap.SugaredLogger {
	return zap.NewNop().Sugar()
}

func (m *InvalidLogger) Debug(args ...interface{}) {
	fmt.Println(args...)
}

func (m *InvalidLogger) Debugf(template string, args ...interface{}) {
	fmt.Printf(template, args...)
}

func (m *InvalidLogger) Debugw(msg string, keysAndValues ...interface{}) {
	fmt.Println(msg, keysAndValues)
}

func (m *InvalidLogger) Info(args ...interface{}) {
	fmt.Println(args...)
}

func (m *InvalidLogger) Infof(template string, args ...interface{}) {
	fmt.Printf(template, args...)
}

func (m *InvalidLogger) Infow(msg string, keysAndValues ...interface{}) {
	fmt.Println(msg, keysAndValues)
}

func (m *InvalidLogger) Warn(args ...interface{}) {
	fmt.Println(args...)
}

func (m *InvalidLogger) Warnf(template string, args ...interface{}) {
	fmt.Printf(template, args...)
}

func (m *InvalidLogger) Warnw(msg string, keysAndValues ...interface{}) {
	fmt.Println(msg, keysAndValues)
}

func (m *InvalidLogger) Error(args ...interface{}) {
	fmt.Println(args...)
}

func (m *InvalidLogger) Errorf(template string, args ...interface{}) {
	fmt.Printf(template, args...)
}

func (m *InvalidLogger) Errorw(msg string, keysAndValues ...interface{}) {
	fmt.Println(msg, keysAndValues)
}

func (m *InvalidLogger) Fatal(args ...interface{}) {
	fmt.Println(args...)
}

func (m *InvalidLogger) Fatalf(template string, args ...interface{}) {
	fmt.Printf(template, args...)
}

func (m *InvalidLogger) Fatalw(msg string, keysAndValues ...interface{}) {
	fmt.Println(msg, keysAndValues)
}

// InvalidLogger fulfills the ZapCompatibleLogger interface with a Sublogger() method to simulate
// calling utils.Sublogger() on an RDK logger.
type MockLogger struct {
	InvalidLogger
	name string
}

func (m *MockLogger) Sublogger(subname string) ZapCompatibleLogger {
	return &InvalidLogger{name: m.name + "." + subname}
}

func TestSubloggerWithZapLogger(t *testing.T) {
	logger := golog.NewTestLogger(t)
	sublogger := Sublogger(logger, "sub")
	test.That(t, sublogger, test.ShouldNotBeNil)
	test.That(t, reflect.TypeOf(sublogger), test.ShouldEqual, reflect.TypeOf(logger))
}

func TestSubloggerWithMockRDKLogger(t *testing.T) {
	logger := &MockLogger{name: "main"}
	sublogger := Sublogger(logger, "sub")
	test.That(t, sublogger, test.ShouldNotBeNil)
	test.That(t, reflect.TypeOf(sublogger), test.ShouldEqual, reflect.TypeOf(logger))
}

func TestSubloggerWithInvalidLogger(t *testing.T) {
	logger := &InvalidLogger{name: "main"}
	sublogger := Sublogger(logger, "sub")
	// Sublogger returns logger (itself) if creating a sublogger fails
	test.That(t, sublogger, test.ShouldEqual, logger)
}
