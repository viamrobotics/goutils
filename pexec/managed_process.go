package pexec

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"

	"go.viam.com/utils"
)

var errAlreadyStopped = errors.New("already stopped")

// defaultStopTimeout is how long to wait in seconds (all stages) between first signaling and finally killing.
const defaultStopTimeout = 10

// A ManagedProcess controls the lifecycle of a single system process. Based on
// its configuration, it will ensure the process is revived if it every unexpectedly
// perishes.
type ManagedProcess interface {
	// ID returns the unique ID of the process.
	ID() string

	// Start starts the process. The given context is only used for one shot processes.
	Start(ctx context.Context) error

	// Stop signals and waits for the process to stop. An error is returned if
	// there's any system level issue stopping the process.
	Stop() error
}

// NewManagedProcess returns a new, unstarted, from the given configuration.
func NewManagedProcess(config ProcessConfig, logger golog.Logger) ManagedProcess {
	logger = logger.Named(fmt.Sprintf("process.%s_%s", config.ID, config.Name))

	var stopSig syscall.Signal
	switch config.StopSignal {
	case "HUP", "SIGHUP", "hangup", "1":
		stopSig = syscall.SIGHUP
	case "INT", "SIGINT", "interrupt", "2":
		stopSig = syscall.SIGINT
	case "QUIT", "SIGQUIT", "quit", "3":
		stopSig = syscall.SIGQUIT
	case "ABRT", "SIGABRT", "aborted", "abort", "6":
		stopSig = syscall.SIGABRT
	case "KILL", "SIGKILL", "killed", "kill", "9":
		stopSig = syscall.SIGKILL
	case "USR1", "SIGUSR1", "user defined signal 1", "10":
		stopSig = syscall.SIGUSR1
	case "USR2", "SIGUSR2", "user defined signal 2", "12":
		stopSig = syscall.SIGUSR1
	case "TERM", "SIGTERM", "terminated", "terminate", "15":
		stopSig = syscall.SIGTERM
	default:
		stopSig = syscall.SIGTERM
	}

	if config.StopTimeout == 0 {
		config.StopTimeout = defaultStopTimeout
	}

	return &managedProcess{
		id:               config.ID,
		name:             config.Name,
		args:             config.Args,
		cwd:              config.CWD,
		oneShot:          config.OneShot,
		shouldLog:        config.Log,
		managingCh:       make(chan struct{}),
		killCh:           make(chan struct{}),
		stopSig:          stopSig,
		stopWaitInterval: time.Duration(config.StopTimeout*0.33) * time.Second,
		logger:           logger,
		logWriter:        config.LogWriter,
	}
}

type managedProcess struct {
	mu sync.Mutex

	id        string
	name      string
	args      []string
	cwd       string
	oneShot   bool
	shouldLog bool
	cmd       *exec.Cmd

	stopped          bool
	managingCh       chan struct{}
	killCh           chan struct{}
	stopSig          os.Signal
	stopWaitInterval time.Duration
	lastWaitErr      error

	logger    golog.Logger
	logWriter io.Writer
}

func (p *managedProcess) ID() string {
	return p.id
}

func (p *managedProcess) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// In the event this Start happened from a restart but a
	// stop happened while we were acquiring the lock, we may
	// need to return early.
	select {
	case <-p.killCh:
		// This will signal to a potential restarter that
		// there's no restart to do.
		return errAlreadyStopped
	default:
	}

	if p.oneShot {
		// Here we use the context since we block on waiting for the command
		// to finish running.
		//nolint:gosec
		cmd := exec.CommandContext(ctx, p.name, p.args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Dir = p.cwd
		var runErr error
		if p.shouldLog || p.logWriter != nil {
			out, err := cmd.CombinedOutput()
			if len(out) > 0 {
				if p.shouldLog {
					p.logger.Debugw("process output", "name", p.name, "output", string(out))
				}
				if p.logWriter != nil {
					if _, err := p.logWriter.Write(out); err != nil && !errors.Is(err, io.ErrClosedPipe) {
						p.logger.Errorw("error writing process output to log writer", "name", p.name, "error", err)
					}
				}
			}
			if err != nil {
				runErr = err
			}
		} else {
			runErr = cmd.Run()
		}
		if runErr == nil {
			return nil
		}
		return errors.Wrapf(runErr, "error running process %q", p.name)
	}

	// This is fully managed so we will control when to kill the process and not
	// use the CommandContext variant.
	//nolint:gosec
	cmd := exec.Command(p.name, p.args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = p.cwd

	var stdOut, stdErr io.ReadCloser
	if p.shouldLog || p.logWriter != nil {
		var err error
		stdOut, err = cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stdErr, err = cmd.StderrPipe()
		if err != nil {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// We have the lock here so it's okay to:
	// 1. Unset the old command, if there was one and let it be GC'd.
	// 2. Assign a new command to be referenced in other places.
	p.cmd = cmd

	// It's okay to not wait for management to start.
	utils.ManagedGo(func() {
		p.manage(stdOut, stdErr)
	}, nil)
	return nil
}

// manage is the watchdog of the process. Any time it detects
// the process has ended unexpectedly, it will restart it. It's
// possible and okay for a restart to be in progress while a Stop
// is happening. As a means simplifying implementation, a restart
// spawns new goroutines by calling Start again and lets the original
// goroutine die off.
func (p *managedProcess) manage(stdOut, stdErr io.ReadCloser) {
	// If no restart is going to happen after this function exits,
	// then we want to notify anyone listening that this process
	// is done being managed. We assume that if we aren't managing,
	// the process is no longer running (it could have double forked though).
	var restarted bool
	defer func() {
		if !restarted {
			close(p.managingCh)
		}
	}()

	// This block here logs as much as possible if it's requested until the
	// pipes are closed.
	stopLogging := make(chan struct{})
	var activeLoggers sync.WaitGroup
	if p.shouldLog || p.logWriter != nil {
		logPipe := func(name string, pipe io.ReadCloser, isErr bool) {
			defer activeLoggers.Done()
			pipeR := bufio.NewReader(pipe)
			logWriterError := false
			for {
				select {
				case <-stopLogging:
					return
				default:
				}
				line, _, err := pipeR.ReadLine()
				if err != nil {
					if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
						p.logger.Errorw("error reading output", "name", name, "error", err)
					}
					return
				}
				if p.shouldLog {
					if isErr {
						p.logger.Errorw("output", "name", name, "data", string(line))
					} else {
						p.logger.Infow("output", "name", name, "data", string(line))
					}
				}
				if p.logWriter != nil && !logWriterError {
					_, err := p.logWriter.Write(line)
					if err == nil {
						_, err = p.logWriter.Write([]byte("\n"))
					}
					if err != nil {
						if !errors.Is(err, io.ErrClosedPipe) {
							p.logger.Debugw("error writing process output to log writer", "name", name, "error", err)
						}
						if !p.shouldLog {
							return
						}
						logWriterError = true
					}
				}
			}
		}
		activeLoggers.Add(2)
		utils.PanicCapturingGo(func() {
			logPipe("StdOut", stdOut, false)
		})
		utils.PanicCapturingGo(func() {
			logPipe("StdErr", stdErr, true)
		})
	}

	err := p.cmd.Wait()
	// This is safe to write to because it is only read in Stop which
	// is waiting for us to stop managing.
	if err == nil {
		p.lastWaitErr = nil
	} else {
		p.lastWaitErr = err
	}
	close(stopLogging)
	activeLoggers.Wait()

	// It's possible that Stop was called and is the reason why Wait returned.
	select {
	case <-p.killCh:
		return
	default:
	}

	// Otherwise, let's try restarting the process.
	if err != nil {
		// Right now we are assuming that any wait error implies the process is no longer
		// alive. TODO(GOUT-8): Verify that
		// this is actually true. If it's false, we could be multiply spawning processes
		// where all are orphaned but one.
		p.logger.Errorw("error waiting for process during manage", "error", err)
	}

	if p.cmd.ProcessState.Exited() {
		p.logger.Infow("process exited before expected", "code", p.cmd.ProcessState.ExitCode())
	} else {
		p.logger.Infow("process exited before expected", "state", p.cmd.ProcessState)
	}
	p.logger.Info("restarting process")

	// Temper ourselves so we aren't constantly restarting if we immediately fail.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	select {
	case <-ticker.C:
	case <-p.killCh:
		return
	}

	err = p.Start(context.Background())
	if err != nil {
		if !errors.Is(err, errAlreadyStopped) {
			// MAYBE(erd): add retry
			p.logger.Errorw("error restarting process", "error", err)
		}
		return
	}
	restarted = true
}

func (p *managedProcess) Stop() error {
	// Minimally hold a lock here so that we can signal the
	// management goroutine to stop. If we were to hold the
	// lock for the duration of the function, we would possibly
	// deadlock with manage trying to restart.
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	close(p.killCh)
	p.stopped = true

	if p.cmd == nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Since p.cmd is mutex guarded and we just signaled the manage
	// goroutine to stop, no new Start can happen and therefore
	// p.cmd can no longer be modified rendering it safe to read
	// without a lock held.

	p.logger.Infof("stopping process %d with %s", p.cmd.Process.Pid, p.stopSig.String())
	// First let's try to directly signal the process.
	if err := p.cmd.Process.Signal(p.stopSig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Wrap(err, "error interrupting process")
	}

	// In case the process didn't stop, or left behind any orphan children in its process group,
	// we now send a signal to everything in the process group after a brief wait.
	timer := time.NewTimer(p.stopWaitInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		p.logger.Infof("stopping entire process group %d with %s", p.cmd.Process.Pid, p.stopSig.String())
		if err := syscall.Kill(-p.cmd.Process.Pid, p.stopSig.(syscall.Signal)); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return errors.Wrap(err, "error interrupting process")
		}
	case <-p.managingCh:
	}

	// Lastly, kill everything in the process group that remains after a longer wait
	timer2 := time.NewTimer(p.stopWaitInterval * 2)
	defer timer2.Stop()
	select {
	case <-timer2.C:
		p.logger.Infof("killing entire process group %d", p.cmd.Process.Pid)
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return errors.Wrap(err, "error killing process")
		}
	case <-p.managingCh:
	}
	<-p.managingCh

	if p.lastWaitErr == nil && p.cmd.ProcessState.Success() {
		return nil
	}

	if p.lastWaitErr != nil {
		var unknownStatus bool
		var errno syscall.Errno
		if errors.As(p.lastWaitErr, &errno) {
			// We lost the race to wait before the signal was caught. We're
			// not going to be able to report any information here about the
			// process stopping, unfortunately.
			if errno == syscall.ECHILD {
				unknownStatus = true
			}
		}

		// This can easily happen if the process does not handle interrupts gracefully
		// and it won't provide us any exit code info.
		switch p.lastWaitErr.Error() {
		case "signal: interrupt", "signal: terminated", "signal: killed":
			unknownStatus = true
		}
		if unknownStatus {
			p.logger.Debug("unable to check exit status")
			return nil
		}
		return p.lastWaitErr
	}
	return errors.Errorf("non-successful exit code: %d", p.cmd.ProcessState.ExitCode())
}
