// Package pexec defines process management utilities to be used as a library within
// a go process wishing to own sub-processes.
//
// It helps manage the lifecycle of processes by keeping them up as long as possible
// when configured.
package pexec

import (
	"encoding/json"
	"io"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"go.viam.com/utils"
)

// defaultStopTimeout is how long to wait in seconds (all stages) between first signaling and finally killing.
const defaultStopTimeout = time.Second * 10

// A ProcessConfig describes how to manage a system process.
type ProcessConfig struct {
	ID          string
	Name        string
	Args        []string
	CWD         string
	OneShot     bool
	Log         bool
	LogWriter   io.Writer
	StopSignal  syscall.Signal
	StopTimeout time.Duration
	// OnCrashHandler will be called when the manage goroutine detects a crash
	// (unexpected stop) of the process. If the returned bool is true, the manage
	// goroutine will attempt to restart the process. Otherwise, the manage
	// goroutine will simply return.
	OnCrashHandler func() bool
}

// Validate ensures all parts of the config are valid.
func (config *ProcessConfig) Validate(path string) error {
	if config.ID == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "id")
	}
	if config.Name == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "name")
	}
	if config.StopTimeout < 100*time.Millisecond && config.StopTimeout != 0 {
		return utils.NewConfigValidationError(path, errors.New("stop_timeout should not be less than 100ms"))
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
	StopSignal  string   `json:"stop_signal,omitempty"`
	StopTimeout string   `json:"stop_timeout,omitempty"`
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
		// OnCrashHandler cannot be specified in JSON.
	}

	if temp.StopTimeout != "" {
		dur, err := time.ParseDuration(temp.StopTimeout)
		if err != nil {
			return err
		}
		config.StopTimeout = dur
	}

	stopSig, err := parseSignal(temp.StopSignal, "stop_signal")
	if err != nil {
		return err
	}
	config.StopSignal = stopSig

	return nil
}

// MarshalJSON converts to json.
func (config ProcessConfig) MarshalJSON() ([]byte, error) {
	var stopSig string
	if config.StopSignal != 0 {
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
		// OnCrashHandler cannot be converted to JSON.
	}
	return json.Marshal(temp)
}
