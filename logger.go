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
	With(args ...interface{}) *zap.SugaredLogger

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
	loggerRet = inp

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

	ret := sublogger.Func.Call([]reflect.Value{reflect.ValueOf(inp), reflect.ValueOf(subname)})
	loggerRet, ok = ret[0].Interface().(ZapCompatibleLogger)
	if !ok {
		inp.Debug("sublogger func returned an unexpected type, returning self")
		return inp
	}

	return loggerRet
}
