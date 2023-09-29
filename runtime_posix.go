//go:build !windows
package utils

import (
	"syscall"
	"os"
	"os/signal"
)

func notifySignals(channel chan os.Signal) {
	signal.Notify(channel, syscall.SIGUSR1)
}
