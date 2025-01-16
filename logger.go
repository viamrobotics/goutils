package utils

import (
	"reflect"

	"github.com/edaniels/golog"
	"go.uber.org/zap"
)

// Logger is used various parts of the package for informational/debugging purposes.
var Logger = golog.Global()

// Debug is helpful to turn on when the library isn't working quite right.
var Debug = false

// ILogger is a basic logging interface for ContextualMain.
type ILogger interface {
	Debug(...interface{})
	Info(...interface{})
	Warn(...interface{})
	Fatal(...interface{})
}

// ZapCompatibleLogger is a basic logging interface for golog.Logger (alias for *zap.SugaredLogger) and RDK loggers.
type ZapCompatibleLogger interface {
	Desugar() *zap.Logger

	// Not defined: Named(name string) *zap.SugaredLogger
	//
	// Use `Sublogger(logger, "name")` instead of calling `Named` directly.

	Debug(args ...interface{})
	Debugf(template string, args ...interface{})
	Debugw(msg string, keysAndValues ...interface{})

	Info(args ...interface{})
	Infof(template string, args ...interface{})
	Infow(msg string, keysAndValues ...interface{})

	Warn(args ...interface{})
	Warnf(template string, args ...interface{})
	Warnw(msg string, keysAndValues ...interface{})

	Error(args ...interface{})
	Errorf(template string, args ...interface{})
	Errorw(msg string, keysAndValues ...interface{})

	Fatal(args ...interface{})
	Fatalf(template string, args ...interface{})
	Fatalw(msg string, keysAndValues ...interface{})
}

// Sublogger creates a sublogger from the given ZapCompatibleLogger instance.
// This function uses reflection to dynamically create a sublogger from the provided logger by
// calling its `Sublogger` method if it is an RDK logger, or its `Named` method if it is a Zap logger.
// If neither method is available, it logs a debug message and returns the original logger.
func Sublogger(inp ZapCompatibleLogger, subname string) (loggerRet ZapCompatibleLogger) {
	loggerRet = inp //nolint:wastedassign

	// loggerRet is initialized to inp as a return value and is intentionally never re-assigned
	// before calling functions that can panic so that defer + recover returns the original logger
	defer func() {
		if r := recover(); r != nil {
			inp.Debugf("panic occurred while creating sublogger: %v, returning self", r)
		}
	}()

	typ := reflect.TypeOf(inp)
	sublogger, ok := typ.MethodByName("Sublogger")
	if !ok {
		sublogger, ok = typ.MethodByName("Named")
		if !ok {
			inp.Debugf("could not create sublogger from logger of type %s, returning self", typ.String())
			return inp
		}
	}

	// When using reflection to call receiver methods, the first argument must be the object.
	// The remaining arguments are the actual function parameters.
	ret := sublogger.Func.Call([]reflect.Value{reflect.ValueOf(inp), reflect.ValueOf(subname)})
	loggerRet, ok = ret[0].Interface().(ZapCompatibleLogger)
	if !ok {
		inp.Debug("sublogger func returned an unexpected type, returning self")
		return inp
	}

	return loggerRet
}

// AddFieldsToLogger attempts to add fields for logging to a given ZapCompatibleLogger instance.
// This function uses reflection to dynamically add fields to the provided logger by
// calling its `WithFields` method if it is an RDK logger. If the logger is not an RDK logger,
// it logs a debug message and returns the original logger.
// Args is expected to be a list of key-value pair(s).
func AddFieldsToLogger(inp ZapCompatibleLogger, args ...interface{}) (loggerRet ZapCompatibleLogger) {
	loggerRet = inp //nolint:wastedassign

	// loggerRet is initialized to inp as a return value and is intentionally never re-assigned
	// before calling functions that can panic so that defer + recover returns the original logger
	defer func() {
		if r := recover(); r != nil {
			inp.Debugf("panic occurred while adding fields to logger: %v, returning self", r)
		}
	}()

	typ := reflect.TypeOf(inp)
	with, ok := typ.MethodByName("WithFields")
	if !ok {
		inp.Debugf("could not add fields to logger of type %s, returning self", typ.String())
		return inp
	}

	// When using reflection to call receiver methods, the first argument must be the object.
	// The remaining arguments are the actual function parameters.
	reflectArgs := make([]reflect.Value, len(args)+1)
	reflectArgs[0] = reflect.ValueOf(inp)
	for i, arg := range args {
		reflectArgs[i+1] = reflect.ValueOf(arg)
	}

	ret := with.Func.Call(reflectArgs)
	loggerRet, ok = ret[0].Interface().(ZapCompatibleLogger)
	if !ok {
		inp.Debug("with func returned an unexpected type, returning self")
		return inp
	}

	return loggerRet
}
