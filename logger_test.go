package utils

import (
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

func (m *InvalidLogger) Debug(args ...interface{}) {
}

func (m *InvalidLogger) Debugf(template string, args ...interface{}) {
}

func (m *InvalidLogger) Debugw(msg string, keysAndValues ...interface{}) {
}

func (m *InvalidLogger) Info(args ...interface{}) {
}

func (m *InvalidLogger) Infof(template string, args ...interface{}) {
}

func (m *InvalidLogger) Infow(msg string, keysAndValues ...interface{}) {
}

func (m *InvalidLogger) Warn(args ...interface{}) {
}

func (m *InvalidLogger) Warnf(template string, args ...interface{}) {
}

func (m *InvalidLogger) Warnw(msg string, keysAndValues ...interface{}) {
}

func (m *InvalidLogger) Error(args ...interface{}) {
}

func (m *InvalidLogger) Errorf(template string, args ...interface{}) {
}

func (m *InvalidLogger) Errorw(msg string, keysAndValues ...interface{}) {
}

func (m *InvalidLogger) Fatal(args ...interface{}) {
}

func (m *InvalidLogger) Fatalf(template string, args ...interface{}) {
}

func (m *InvalidLogger) Fatalw(msg string, keysAndValues ...interface{}) {
}

// MockLogger fulfills the ZapCompatibleLogger interface by extending InvalidLogger with a Sublogger() method. This type
// is used to simulate calling utils.Sublogger() on an RDK logger.
type MockLogger struct {
	InvalidLogger
	Name string
}

func (m *MockLogger) Sublogger(subname string) ZapCompatibleLogger {
	return &MockLogger{Name: m.Name + "." + subname}
}

func (m *MockLogger) WithFields(args ...interface{}) {
	m.Name = "WithFields called"
}

func TestSubloggerWithZapLogger(t *testing.T) {
	logger := golog.NewTestLogger(t)
	sublogger := Sublogger(logger, "sub")
	test.That(t, sublogger, test.ShouldNotBeNil)
	test.That(t, sublogger, test.ShouldNotEqual, logger)
	test.That(t, reflect.TypeOf(sublogger), test.ShouldEqual, reflect.TypeOf(logger))
}

func TestSubloggerWithMockRDKLogger(t *testing.T) {
	logger := &MockLogger{Name: "main"}
	sublogger := Sublogger(logger, "sub")
	test.That(t, sublogger, test.ShouldNotBeNil)
	test.That(t, sublogger, test.ShouldNotEqual, logger)
	test.That(t, reflect.TypeOf(sublogger), test.ShouldEqual, reflect.TypeOf(logger))
	test.That(t, sublogger.(*MockLogger).Name, test.ShouldEqual, "main.sub")
}

func TestSubloggerWithInvalidLogger(t *testing.T) {
	logger := &InvalidLogger{name: "main"}
	sublogger := Sublogger(logger, "sub")
	// Sublogger returns logger (itself) if creating a sublogger fails, which we expect
	test.That(t, sublogger, test.ShouldEqual, logger)
}

func TestLogWithZapLogger(t *testing.T) {
	logger := golog.NewTestLogger(t)
	loggerWith := AddFieldsToLogger(logger, "key", "value")
	test.That(t, loggerWith, test.ShouldNotBeNil)
	test.That(t, loggerWith, test.ShouldNotEqual, logger)
	test.That(t, reflect.TypeOf(loggerWith), test.ShouldEqual, reflect.TypeOf(logger))
}

func TestLogWithMockRDKLogger(t *testing.T) {
	logger := &MockLogger{Name: "main"}
	loggerWith := AddFieldsToLogger(logger, "key", "value")
	test.That(t, loggerWith, test.ShouldNotBeNil)
	test.That(t, loggerWith, test.ShouldEqual, logger) // MockLogger modifies the logger in place
	test.That(t, reflect.TypeOf(loggerWith), test.ShouldEqual, reflect.TypeOf(logger))
	test.That(t, loggerWith.(*MockLogger).Name, test.ShouldEqual, "WithFields called")
}

func TestLogWithInvalidLogger(t *testing.T) {
	logger := &InvalidLogger{name: "main"}
	loggerWith := AddFieldsToLogger(logger, "key", "value")
	// With returns logger (itself) if adding fields fails, which we expect
	test.That(t, loggerWith, test.ShouldEqual, logger)
}
