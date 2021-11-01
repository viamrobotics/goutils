package rpcwebrtc

import (
	"github.com/edaniels/golog"
	"github.com/pion/logging"
	"go.uber.org/zap"
)

// LoggerFactory wraps a golog.Logger for use with pion's webrtc logging system.
type LoggerFactory struct {
	Logger golog.Logger
}

type logger struct {
	logger golog.Logger
}

func (l logger) loggerWithSkip() golog.Logger {
	return l.logger.Desugar().WithOptions(zap.AddCallerSkip(1)).Sugar()
}

func (l logger) Trace(msg string) {
	l.loggerWithSkip().Debug(msg)
}

func (l logger) Tracef(format string, args ...interface{}) {
	l.loggerWithSkip().Debugf(format, args...)
}

func (l logger) Debug(msg string) {
	l.loggerWithSkip().Debug(msg)
}

func (l logger) Debugf(format string, args ...interface{}) {
	l.loggerWithSkip().Debugf(format, args...)
}

func (l logger) Info(msg string) {
	l.loggerWithSkip().Info(msg)
}

func (l logger) Infof(format string, args ...interface{}) {
	l.loggerWithSkip().Infof(format, args...)
}

func (l logger) Warn(msg string) {
	l.loggerWithSkip().Warn(msg)
}

func (l logger) Warnf(format string, args ...interface{}) {
	l.loggerWithSkip().Warnf(format, args...)
}

func (l logger) Error(msg string) {
	l.loggerWithSkip().Error(msg)
}

func (l logger) Errorf(format string, args ...interface{}) {
	l.loggerWithSkip().Errorf(format, args...)
}

// NewLogger returns a new webrtc logger under the given scope.
func (lf LoggerFactory) NewLogger(scope string) logging.LeveledLogger {
	return logger{lf.Logger.Named(scope)}
}
