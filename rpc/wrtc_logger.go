package rpc

import (
	"github.com/edaniels/golog"
	"github.com/pion/logging"
	"go.uber.org/zap"
)

// WebRTCLoggerFactory wraps a golog.Logger for use with pion's webrtc logging system.
type WebRTCLoggerFactory struct {
	Logger golog.Logger
}

type webrtcLogger struct {
	logger golog.Logger
}

func (l webrtcLogger) loggerWithSkip() golog.Logger {
	return l.logger.Desugar().WithOptions(zap.AddCallerSkip(1)).Sugar()
}

func (l webrtcLogger) Trace(msg string) {
	l.loggerWithSkip().Debug(msg)
}

func (l webrtcLogger) Tracef(format string, args ...interface{}) {
	l.loggerWithSkip().Debugf(format, args...)
}

func (l webrtcLogger) Debug(msg string) {
	l.loggerWithSkip().Debug(msg)
}

func (l webrtcLogger) Debugf(format string, args ...interface{}) {
	l.loggerWithSkip().Debugf(format, args...)
}

func (l webrtcLogger) Info(msg string) {
	l.loggerWithSkip().Info(msg)
}

func (l webrtcLogger) Infof(format string, args ...interface{}) {
	l.loggerWithSkip().Infof(format, args...)
}

func (l webrtcLogger) Warn(msg string) {
	l.loggerWithSkip().Warn(msg)
}

func (l webrtcLogger) Warnf(format string, args ...interface{}) {
	l.loggerWithSkip().Warnf(format, args...)
}

func (l webrtcLogger) Error(msg string) {
	l.loggerWithSkip().Error(msg)
}

func (l webrtcLogger) Errorf(format string, args ...interface{}) {
	l.loggerWithSkip().Errorf(format, args...)
}

// NewLogger returns a new webrtc logger under the given scope.
func (lf WebRTCLoggerFactory) NewLogger(scope string) logging.LeveledLogger {
	return webrtcLogger{lf.Logger.Named(scope)}
}
