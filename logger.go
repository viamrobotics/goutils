package utils

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/edaniels/golog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
)

// Logger is used various parts of the package for informational/debugging purposes.
var Logger = golog.Global()

// Debug is helpful to turn on when the library isn't working quite right.
var Debug = false

var GlobalLogLevel = zap.NewAtomicLevelAt(zap.InfoLevel)

const DefaultTimeFormatStr = "2006-01-02T15:04:05.000Z0700"

// ZapCompatibleLogger is a basic logging interface.
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

type AtomicLevel struct {
	val *atomic.Int32
}

// Get returns the level.
func (level AtomicLevel) Get() Level {
	return Level(level.val.Load())
}

type Level int

const (
	// This numbering scheme serves two purposes:
	//   - A statement is logged if its log level matches or exceeds the configured level. I.e:
	//     Statement(WARN) >= LogConfig(INFO) would be logged because "1" > "0".
	//   - INFO is the default level. So we start counting at DEBUG=-1 such that INFO is given Go's
	//     zero-value.

	// DEBUG log level.
	DEBUG Level = iota - 1
	// INFO log level.
	INFO
	// WARN log level.
	WARN
	// ERROR log level.
	ERROR
)

// AsZap converts the Level to a `zapcore.Level`.
func (level Level) AsZap() zapcore.Level {
	switch level {
	case DEBUG:
		return zapcore.DebugLevel
	case INFO:
		return zapcore.InfoLevel
	case WARN:
		return zapcore.WarnLevel
	case ERROR:
		return zapcore.ErrorLevel
	}

	panic(fmt.Sprintf("unreachable: %d", level))
}

type RDKLogger interface {
	ZapCompatibleLogger
	GetName() string
	GetLevel() Level
	GetAppenders() []Appender
}

type Appender interface {
	// Write submits a structured log entry to the appender for logging.
	Write(zapcore.Entry, []zapcore.Field) error
	// Sync is for signaling that any buffered logs to `Write` should be flushed. E.g: at shutdown.
	Sync() error
}

type testAppender struct {
	tb testing.TB
}

// The input `caller` must satisfy `caller.Defined == true`.
func callerToString(caller *zapcore.EntryCaller) string {
	// The file returned by `runtime.Caller` is a full path and always contains '/' to separate
	// directories. Including on windows. We only want to keep the `<package>/<file>` part of the
	// path. We use a stateful lambda to count back two '/' runes.
	cnt := 0
	idx := strings.LastIndexFunc(caller.File, func(rn rune) bool {
		if rn == '/' {
			cnt++
		}

		if cnt == 2 {
			return true
		}

		return false
	})

	// If idx >= 0, then we add 1 to trim the leading '/'.
	// If idx == -1 (not found), we add 1 to return the entire file.
	return fmt.Sprintf("%s:%d", caller.File[idx+1:], caller.Line)
}

// Write outputs the log entry to the underlying test object `Log` method.
func (tapp *testAppender) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	tapp.tb.Helper()
	const maxLength = 10
	toPrint := make([]string, 0, maxLength)
	toPrint = append(toPrint, entry.Time.Format(DefaultTimeFormatStr))

	toPrint = append(toPrint, strings.ToUpper(entry.Level.String()))
	toPrint = append(toPrint, entry.LoggerName)
	if entry.Caller.Defined {
		toPrint = append(toPrint, callerToString(&entry.Caller))
	}
	toPrint = append(toPrint, entry.Message)
	if len(fields) == 0 {
		tapp.tb.Log(strings.Join(toPrint, "\t"))
		return nil
	}

	// Use zap's json encoder which will encode our slice of fields in-order. As opposed to the
	// random iteration order of a map. Call it with an empty Entry object such that only the fields
	// become "map-ified".
	jsonEncoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{SkipLineEnding: true})
	buf, err := jsonEncoder.EncodeEntry(zapcore.Entry{}, fields)
	if err != nil {
		// Log what we have and return the error.
		tapp.tb.Log(strings.Join(toPrint, "\t"))
		return err
	}
	toPrint = append(toPrint, string(buf.Bytes()))
	tapp.tb.Log(strings.Join(toPrint, "\t"))
	return nil
}

// Sync is a no-op.
func (tapp *testAppender) Sync() error {
	return nil
}

// NewZapLoggerConfig returns a new default logger config.
func NewZapLoggerConfig() zap.Config {
	// from https://github.com/uber-go/zap/blob/2314926ec34c23ee21f3dd4399438469668f8097/config.go#L135
	// but disable stacktraces, use same keys as prod, and color levels.
	return zap.Config{
		Level:    GlobalLogLevel,
		Encoding: "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		DisableStacktrace: true,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
	}
}

func AsZap(lg ZapCompatibleLogger) *zap.SugaredLogger {
	switch logger := lg.(type) {
	case *zap.SugaredLogger:
		// golog.Logger is a type alias for *zap.SugaredLogger and is captured by this.
		return logger
	case RDKLogger:
		// When downconverting to a SugaredLogger, copy those that implement the `zapcore.Core`
		// interface. This includes the net logger for viam servers and the observed logs for tests.
		var copiedCores []zapcore.Core

		// When we find a `testAppender`, copy the underlying `testing.TB` object and construct a
		// `zaptest.NewLogger` from it.
		var testingObj testing.TB
		for _, appender := range logger.GetAppenders() {
			if core, ok := appender.(zapcore.Core); ok {
				copiedCores = append(copiedCores, core)
			}
			if testAppender, ok := appender.(*testAppender); ok {
				testingObj = testAppender.tb
			}
		}

		var ret *zap.SugaredLogger
		if testingObj == nil {
			config := NewZapLoggerConfig()
			// Use the global zap `AtomicLevel` such that the constructed zap logger can observe changes to
			// the debug flag.
			if logger.GetLevel() == DEBUG {
				config.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
			} else {
				config.Level = GlobalLogLevel
			}
			ret = zap.Must(config.Build()).Sugar().Named(logger.GetName())
		} else {
			ret = zaptest.NewLogger(testingObj,
				zaptest.WrapOptions(zap.AddCaller()),
				zaptest.Level(logger.GetLevel().AsZap()),
			).Sugar().Named(logger.GetName())
		}

		for _, core := range copiedCores {
			ret = ret.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
				return zapcore.NewTee(c, core)
			}))
		}

		return ret
	default:
		logger.Warnf("Unknown logger type, creating a new Viam Logger. Unknown type: %T", logger)
		return golog.Global()
	}
}
