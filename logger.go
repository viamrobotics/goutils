package utils

import (
	"github.com/edaniels/golog"
)

// Logger is used various parts of the package for informational/debugging purposes.
var Logger = golog.Global()

// Debug is helpful to turn on when the library isn't working quite right.
var Debug = false

// ILogger is a basic logging interface.
type ILogger interface {
	Debug(...interface{})
	Info(...interface{})
	Warn(...interface{})
	Fatal(...interface{})
}
