package utils

import (
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
	Named(name string) *zap.SugaredLogger
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
