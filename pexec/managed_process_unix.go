//go:build linux || darwin

package pexec

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/pkg/errors"
)

func sigStr(sig syscall.Signal) string {
	//nolint:exhaustive
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return "<UNKNOWN>"
	}
}

var knownSignals = []syscall.Signal{
	syscall.SIGHUP,
	syscall.SIGINT,
	syscall.SIGQUIT,
	syscall.SIGABRT,
	syscall.SIGUSR1,
	syscall.SIGUSR2,
	syscall.SIGTERM,
}

func parseSignal(sigStr, name string) (syscall.Signal, error) {
	switch sigStr {
	case "":
		return 0, nil
	case "HUP", "SIGHUP", "hangup", "1":
		return syscall.SIGHUP, nil
	case "INT", "SIGINT", "interrupt", "2":
		return syscall.SIGINT, nil
	case "QUIT", "SIGQUIT", "quit", "3":
		return syscall.SIGQUIT, nil
	case "ABRT", "SIGABRT", "aborted", "abort", "6":
		return syscall.SIGABRT, nil
	case "KILL", "SIGKILL", "killed", "kill", "9":
		return syscall.SIGKILL, nil
	case "TERM", "SIGTERM", "terminated", "terminate", "15":
		return syscall.SIGTERM, nil
	default:
		return 0, errors.Errorf("unknown %q name", sigStr)
	}
}

func (p *managedProcess) sysProcAttr() (*syscall.SysProcAttr, error) {
	attrs := &syscall.SysProcAttr{Setpgid: true}
	if len(p.username) > 0 {
		user, err := user.Lookup(p.username)
		if err != nil {
			return nil, err
		}
		val, err := strconv.ParseUint(user.Uid, 10, 32)
		if err != nil {
			return nil, err
		}
		attrs.Credential = &syscall.Credential{}
		attrs.Credential.Uid = uint32(val)
		val, err = strconv.ParseUint(user.Gid, 10, 32)
		if err != nil {
			return nil, err
		}
		attrs.Credential.Gid = uint32(val)
	}
	return attrs, nil
}

func (p *managedProcess) status() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd.Process.Signal(syscall.Signal(0))
}

// kill attempts to stop the managedProcess.
// The boolean return value indicates whether the process was force killed or not. If the process is already done
// or no longer exist, a special ProcessNotExistsError is returned.
func (p *managedProcess) kill() (bool, error) {
	p.logger.Infof("stopping process %d with signal %s", p.cmd.Process.Pid, p.stopSig)
	// First let's try to directly signal the process.
	if err := p.cmd.Process.Signal(p.stopSig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return false, errors.Wrapf(err, "error signaling process %d with signal %s", p.cmd.Process.Pid, p.stopSig)
	}

	// In case the process didn't stop, or left behind any orphan children in its process group,
	// we now send a signal to everything in the process group after a brief wait.
	timer := time.NewTimer(p.stopWaitInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		p.logger.Infof("stopping entire process group %d with signal %s", p.cmd.Process.Pid, p.stopSig)
		if err := p.killGroup(p.stopSig); err != nil {
			return false, err
		}
	case <-p.managingCh:
		timer.Stop()
	}

	// Lastly, kill everything in the process group that remains after a longer wait
	var forceKilled bool
	timer2 := time.NewTimer(p.stopWaitInterval * 2)
	defer timer2.Stop()
	select {
	case <-timer2.C:
		p.logger.Infof("killing entire process group %d", p.cmd.Process.Pid)
		if err := p.killGroup(syscall.SIGKILL); err != nil {
			return false, err
		}
		forceKilled = true
	case <-p.managingCh:
		timer2.Stop()
	}

	return forceKilled, nil
}

// killGroup signals every process in the managed process's group. Landing on nothing is
// reported as a ProcessNotExistsError rather than as a failure to stop: kill has already
// signaled the process itself by this point, so this sweep exists only to catch orphaned
// children, and an empty group means it has none to catch.
func (p *managedProcess) killGroup(sig syscall.Signal) error {
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		if processGroupGone(err) {
			return &ProcessNotExistsError{err}
		}
		return errors.Wrapf(err, "error signaling process group %d with signal %s", p.cmd.Process.Pid, sig)
	}
	return nil
}

// processGroupGone reports whether err from signaling a process group means there was nothing
// left in it to signal.
//
// The two ways that happens are not reported alike. Once the process has been reaped its group
// is gone and kill(2) answers ESRCH, but in the window where it has exited and not yet been
// reaped the group still holds a zombie: Linux signals that group without complaint, while
// Darwin finds a member it cannot signal and answers EPERM. Reading EPERM as a permission
// failure would therefore turn a clean shutdown into an error on macOS alone.
func processGroupGone(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ESRCH || errno == syscall.EPERM
	}
	return errors.Is(err, os.ErrProcessDone)
}

// forceKillGroup kills everything in the process group. This will not wait for completion and may result the
// kill becoming a zombie process.
func (p *managedProcess) forceKillGroup() error {
	p.logger.Infof("killing entire process group %d", p.cmd.Process.Pid)
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
}
