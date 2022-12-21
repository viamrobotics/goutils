// Package pexec defines process management utilities to be used as a library within
// a go process wishing to own sub-processes.
//
// It helps manage the lifecycle of processes by keeping them up as long as possible
// when configured.
package pexec

import (
	"encoding/json"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"go.viam.com/utils"
)

// defaultStopTimeout is how long to wait in seconds (all stages) between first signaling and finally killing.
const defaultStopTimeout = time.Second * 10

// A ProcessConfig describes how to manage a system process.
type ProcessConfig struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Args        []string      `json:"args"`
	CWD         string        `json:"cwd"`
	OneShot     bool          `json:"one_shot"`
	Log         bool          `json:"log"`
	LogWriter   io.Writer     `json:"-"`
	StopSignal  os.Signal     `json:"stop_signal"`
	StopTimeout time.Duration `json:"stop_timeout"`
}

// Validate ensures all parts of the config are valid.
func (config *ProcessConfig) Validate(path string) error {
	if config.StopTimeout == 0 {
		config.StopTimeout = defaultStopTimeout
	} else if config.StopTimeout <= 100*time.Millisecond {
		return utils.NewConfigValidationError(path, errors.New("stop_timeout should not be less than 100ms"))
	}
	if config.StopSignal == nil {
		config.StopSignal = syscall.SIGTERM
	}
	if config.ID == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "id")
	}
	if config.Name == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "name")
	}
	return nil
}

// Note: keep this in sync with json-supported fields in ProcessConfig.
type configData struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Args        []string `json:"args"`
	CWD         string   `json:"cwd"`
	OneShot     bool     `json:"one_shot"`
	Log         bool     `json:"log"`
	StopSignal  string   `json:"stop_signal"`
	StopTimeout string   `json:"stop_timeout"`
}

// UnmarshalJSON parses incoming json.
func (config *ProcessConfig) UnmarshalJSON(data []byte) error {
	var temp configData
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	*config = ProcessConfig{
		ID:      temp.ID,
		Name:    temp.Name,
		Args:    temp.Args,
		CWD:     temp.CWD,
		OneShot: temp.OneShot,
		Log:     temp.Log,
	}

	if temp.StopTimeout != "" {
		dur, err := time.ParseDuration(temp.StopTimeout)
		if err != nil {
			return err
		}
		config.StopTimeout = dur
	}

	switch temp.StopSignal {
	case "":
		config.StopSignal = syscall.SIGTERM
	case "HUP", "SIGHUP", "hangup", "1":
		config.StopSignal = syscall.SIGHUP
	case "INT", "SIGINT", "interrupt", "2":
		config.StopSignal = syscall.SIGINT
	case "QUIT", "SIGQUIT", "quit", "3":
		config.StopSignal = syscall.SIGQUIT
	case "ABRT", "SIGABRT", "aborted", "abort", "6":
		config.StopSignal = syscall.SIGABRT
	case "KILL", "SIGKILL", "killed", "kill", "9":
		config.StopSignal = syscall.SIGKILL
	case "USR1", "SIGUSR1", "user defined signal 1", "10":
		config.StopSignal = syscall.SIGUSR1
	case "USR2", "SIGUSR2", "user defined signal 2", "12":
		config.StopSignal = syscall.SIGUSR1
	case "TERM", "SIGTERM", "terminated", "terminate", "15":
		config.StopSignal = syscall.SIGTERM
	default:
		return errors.New("unknown stop_signal name")
	}

	return nil
}

// MarshalJSON converts to json.
func (config ProcessConfig) MarshalJSON() ([]byte, error) {
	var stopSig string
	if config.StopSignal != nil {
		stopSig = config.StopSignal.String()
	}
	temp := configData{
		ID:          config.ID,
		Name:        config.Name,
		Args:        config.Args,
		CWD:         config.CWD,
		OneShot:     config.OneShot,
		Log:         config.Log,
		StopSignal:  stopSig,
		StopTimeout: config.StopTimeout.String(),
	}
	return json.Marshal(temp)
}
