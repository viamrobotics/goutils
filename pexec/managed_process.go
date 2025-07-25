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

	"github.com/pkg/errors"

	"go.viam.com/utils"
)

var errAlreadyStopped = errors.New("already stopped")

// UnexpectedExitHandler is the signature for functions that can optionally be
// provided to run when a managed process unexpectedly exits. The return value
// indicates whether pexec should continue with its own attempt to restart the
// process: true means pexec will attempt its own restart, false means the
// no restart will be attempted and the process will remain dead.
type UnexpectedExitHandler = func(exitCode int) bool

// A ManagedProcess controls the lifecycle of a single system process. Based on
// its configuration, it will ensure the process is revived if it every unexpectedly
// perishes.
type ManagedProcess interface {
	// ID returns the unique ID of the process.
	ID() string

	// Start starts the process. The given context is only used for one shot processes.
	Start(ctx context.Context) error

	// Stop signals and waits for the process to stop. An error is returned if the process may still
	// be running.
	Stop() error

	// KillGroup will attempt to kill the process group and not wait for completion. Only use this if
	// comfortable with leaking resources (in cases where exiting the program as quickly as possible is desired).
	KillGroup()

	// Status return nil when the process is both alive and owned.
	// If err is non-nil, process may be a) alive but not owned or b) dead.
	Status() error

	// UnixPid returns the pid of the process. This method returns an error if the pid is
	// unknown. For example, if the process hasn't been `Start`ed yet. Or if not on a unix system.
	UnixPid() (int, error)
}

// NewManagedProcess returns a new, unstarted, from the given configuration.
func NewManagedProcess(config ProcessConfig, logger utils.ZapCompatibleLogger) ManagedProcess {
	// NOTE(benjirewis): config.ID maps to the module name in the module
	// manager's usage of this method and the passed-in ProcessConfig.
	logger = utils.Sublogger(logger, config.ID)

	if config.StopSignal == 0 {
		config.StopSignal = syscall.SIGTERM
	}

	if config.StopTimeout == 0 {
		config.StopTimeout = defaultStopTimeout
	}

	// From os/exec/exec.go:
	//  If Env contains duplicate environment keys, only the last
	//  value in the slice for each duplicate key is used.
	env := os.Environ()
	for key, value := range config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	return &managedProcess{
		id:               config.ID,
		name:             config.Name,
		args:             config.Args,
		cwd:              config.CWD,
		oneShot:          config.OneShot,
		username:         config.Username,
		env:              env,
		shouldLog:        config.Log,
		onUnexpectedExit: config.OnUnexpectedExit,
		managingCh:       make(chan struct{}),
		killCh:           make(chan struct{}),
		stopSig:          config.StopSignal,
		stopWaitInterval: config.StopTimeout / time.Duration(3),
		logger:           logger,
		logWriter:        config.LogWriter,
		stdoutLogger:     config.StdOutLogger,
		stderrLogger:     config.StdErrLogger,
	}
}

type managedProcess struct {
	mu sync.Mutex

	id        string
	name      string
	args      []string
	cwd       string
	oneShot   bool
	username  string
	env       []string
	shouldLog bool
	cmd       *exec.Cmd

	onUnexpectedExit UnexpectedExitHandler
	managingCh       chan struct{}
	killCh           chan struct{}
	stopSig          syscall.Signal
	stopWaitInterval time.Duration
	lastWaitErr      error

	logger       utils.ZapCompatibleLogger
	logWriter    io.Writer
	stdoutLogger utils.ZapCompatibleLogger
	stderrLogger utils.ZapCompatibleLogger
}

func (p *managedProcess) ID() string {
	return p.id
}

func (p *managedProcess) UnixPid() (int, error) {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0, errors.New("Process not started")
	}
	return p.cmd.Process.Pid, nil
}

func (p *managedProcess) Status() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd.Process.Signal(syscall.Signal(0))
}

func (p *managedProcess) validateCWD() error {
	if p.cwd == "" {
		return nil
	}

	_, lstaterr := os.Lstat(p.cwd)
	if lstaterr == nil {
		return nil
	}

	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return fmt.Errorf(
			`error setting process working directory to %q: %w; also error getting current working directory: %w`,
			p.cwd, lstaterr, cwdErr,
		)
	}

	return fmt.Errorf(
		`error setting process working directory to %q from current working directory %q: %w`,
		p.cwd, cwd, lstaterr,
	)
}

func (p *managedProcess) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.start(ctx)
}

// This internal version of start must be called with the process lock held.
func (p *managedProcess) start(ctx context.Context) error {
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

	if err := p.validateCWD(); err != nil {
		return err
	}

	if p.oneShot {
		// Here we use the context since we block on waiting for the command
		// to finish running.
		//nolint:gosec
		cmd := exec.CommandContext(ctx, p.name, p.args...)
		var err error
		if cmd.SysProcAttr, err = p.sysProcAttr(); err != nil {
			return err
		}
		cmd.Env = p.env
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
	var err error
	if cmd.SysProcAttr, err = p.sysProcAttr(); err != nil {
		return err
	}
	cmd.Env = p.env
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

// manage is the watchdog of the process. If the process has ended
// unexpectedly, onUnexpectedExit will be called. If onUnexpectedExit is unset
// or returns true, manage will restart the process. Note that onUnexpectedExit
// may be called multiple times if it returns true. It's possible and okay for
// a restart to be in progress while a Stop is happening. As a means of
// simplifying implementation, a restart spawns new goroutines by calling Start
// again and lets the original goroutine die off.
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

	stopAndDrainLoggers := p.startLoggers(stdOut, stdErr)

	err := p.cmd.Wait()
	// This is safe to write to because it is only read in Stop which
	// is waiting for us to stop managing.
	p.lastWaitErr = err

	stopAndDrainLoggers()

	// Take the lock to prevent a race where a crashed process restarts even
	// though Stop is called.
	p.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.mu.Unlock()
		}
	}()

	// It's possible that Stop was called and is the reason why Wait returned.
	select {
	case <-p.killCh:
		return
	default:
	}

	// Run onUnexpectedExit if it exists. Do not attempt restart if
	// onUnexpectedExit returns false. Drop the lock to avoid deadlocking other
	// goroutines that my try to call Stop while we're handling a crash.
	if p.onUnexpectedExit != nil {
		p.mu.Unlock()
		locked = false
		if !p.onUnexpectedExit(p.cmd.ProcessState.ExitCode()) {
			return
		}
		p.mu.Lock()
		locked = true
		// Dropped the lock to call the oue handler, check if we were stopped during
		// that time.
		select {
		case <-p.killCh:
			return
		default:
		}
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

	// Use the internal version of start since we already hold the lock.
	err = p.start(context.Background())
	if err != nil {
		if !errors.Is(err, errAlreadyStopped) {
			p.logger.Errorw("error restarting process", "error", err)
		}
		return
	}
	restarted = true
}

// This helper function is only meant to be called from manage. If logging is
// enabled it creates goroutines that log as much as possible until the pipes
// are closed. It returns a function that terminates logging and blocks until
// the loggers drain.
func (p *managedProcess) startLoggers(stdOut, stdErr io.ReadCloser) func() {
	if !p.shouldLog && p.logWriter == nil {
		// No logging enabled, return a noop func so the caller can unconditionally
		// invoke it.
		return func() {}
	}

	stopLogging := make(chan struct{})
	var activeLoggers sync.WaitGroup
	activeLoggers.Add(2)
	logPipe := func(name string, pipe io.ReadCloser, isErr bool, logger utils.ZapCompatibleLogger) {
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
					logger.Error("\n\\_ " + string(line))
				} else {
					logger.Info("\n\\_ " + string(line))
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

	utils.PanicCapturingGo(func() {
		name := "StdOut"
		var logger utils.ZapCompatibleLogger
		if p.stdoutLogger != nil {
			logger = p.stdoutLogger
		} else {
			logger = utils.Sublogger(p.logger, name)
		}
		logPipe(name, stdOut, false, logger)
	})
	utils.PanicCapturingGo(func() {
		name := "StdErr"
		var logger utils.ZapCompatibleLogger
		if p.stderrLogger != nil {
			logger = p.stderrLogger
		} else {
			logger = utils.Sublogger(p.logger, name)
		}
		logPipe(name, stdErr, true, logger)
	})

	return func() {
		close(stopLogging)
		activeLoggers.Wait()
	}
}

func (p *managedProcess) Stop() error {
	p.mu.Lock()

	// Return early if the process has already been killed.
	select {
	case <-p.killCh:
		p.mu.Unlock()
		if p.cmd != nil {
			// Avoid deadlocking if Stop was called before Start while blocking all
			// calls to Stop that follow Start until the management goroutine shuts
			// down.
			<-p.managingCh
		}
		return nil
	default:
	}

	close(p.killCh)

	// All relevant methods wait on the lock we hold and will not attempt to
	// (re)start the process now that we closed killch, so we can safely drop the
	// lock to unblock other calls while we proceed with shutown.
	p.mu.Unlock()

	// Nothing to do.
	if p.cmd == nil {
		return nil
	}

	_, err := p.kill()
	if err != nil {
		return err
	}
	<-p.managingCh

	if p.lastWaitErr != nil {
		p.logger.Warnw("Stopped module did not exit cleanly", "err", p.lastWaitErr)
	}

	return nil
}

// KillGroup kills the process group.
func (p *managedProcess) KillGroup() {
	// Minimally hold a lock here so that we can signal the
	// management goroutine to stop. We will attempt to kill the
	// process even if p.stopped is true.
	p.mu.Lock()
	select {
	case <-p.killCh:
	default:
		close(p.killCh)
	}

	if p.cmd == nil {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	// Since p.cmd is mutex guarded and we just signaled the manage
	// goroutine to stop, no new Start can happen and therefore
	// p.cmd can no longer be modified rendering it safe to read
	// without a lock held.
	// We are intentionally not checking the error here, we are already
	// in a bad state.
	//nolint:errcheck,gosec
	p.forceKillGroup()
}
