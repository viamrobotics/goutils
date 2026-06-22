package rpc

import (
	"strings"

	"github.com/pion/logging"
	"go.uber.org/zap"

	"go.viam.com/utils"
)

// demotedPionLogSubstrings lists substrings of pion log messages that we demote from
// error to debug. These are known-benign messages that recur during normal operation
// and do not reflect connection health, so logging them at error is misleading noise.
var demotedPionLogSubstrings = []string{
	// pion's TURN client logs "Fail to refresh permissions" once a relay allocation's
	// time-limited credential expires and the TURN server rejects the periodic refresh.
	// This does not reflect connection health: an in-use relay recovers by re-dialing with
	// fresh credentials, and an idle/unused allocation (a connection that selected a direct
	// candidate pair but still gathered a relay candidate) is unaffected on its actual data path.
	"Fail to refresh permissions",
}

func shouldDemotePionLog(msg string) bool {
	for _, sub := range demotedPionLogSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

// WebRTCLoggerFactory wraps a utils.ZapCompatibleLogger for use with pion's webrtc logging system.
type WebRTCLoggerFactory struct {
	Logger utils.ZapCompatibleLogger
}

type webrtcLogger struct {
	logger utils.ZapCompatibleLogger
}

func (l webrtcLogger) loggerWithSkip() utils.ZapCompatibleLogger {
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
	return webrtcLogger{utils.Sublogger(lf.Logger, scope)}
}

// demotingLoggerFactory wraps another pion logging.LoggerFactory and demotes known-benign
// error logs (see demotedPionLogSubstrings) to debug, passing every other log through to the
// wrapped factory unchanged.
type demotingLoggerFactory struct {
	base logging.LoggerFactory
}

func (f demotingLoggerFactory) NewLogger(scope string) logging.LeveledLogger {
	return demotingLogger{f.base.NewLogger(scope)}
}

// demotingLogger demotes known-benign error messages (see demotedPionLogSubstrings) to
// debug. All other levels, and all other messages, are inherited unchanged from the embedded
// logger.
type demotingLogger struct {
	logging.LeveledLogger
}

func (l demotingLogger) Error(msg string) {
	if shouldDemotePionLog(msg) {
		l.LeveledLogger.Debug(msg)
		return
	}
	l.LeveledLogger.Error(msg)
}

func (l demotingLogger) Errorf(format string, args ...interface{}) {
	if shouldDemotePionLog(format) {
		l.LeveledLogger.Debugf(format, args...)
		return
	}
	l.LeveledLogger.Errorf(format, args...)
}
