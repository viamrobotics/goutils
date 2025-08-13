//go:build windows

package pexec

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

func sigStr(sig syscall.Signal) string {
	return "<UNKNOWN>"
}

var knownSignals []syscall.Signal = nil

func parseSignal(sigStr, name string) (syscall.Signal, error) {
	if sigStr == "" {
		return 0, nil
	}
	return 0, errors.New("signals not supported on Windows")
}

func (p *managedProcess) sysProcAttr() (*syscall.SysProcAttr, error) {
	ret := &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if len(p.username) > 0 {
		return nil, errors.Errorf("can't run as user %s, not supported yet on windows", p.username)
	}
	return ret, nil
}

// kill attempts to stop the managedProcess.
// The boolean return value indicates whether the process was force killed or not. If the process is already done
// or no longer exist, a special ProcessNotExistsError is returned.
func (p *managedProcess) kill() (bool, error) {
	// NOTE: the first kill attempt behavior is different on unix. If the first attempt to kill the
	// process results in os.ErrProcessDone on unix, no error is returned. Only if a process
	// is not found when trying to kill its tree or force kill it, then &ProcessNotExistsError
	// is returned.
	// On windows, we will always return this error, even when the first attempt failed.
	const mustForce = "This process can only be terminated forcefully"
	pidStr := strconv.Itoa(p.cmd.Process.Pid)
	p.logger.Infof("killing process %d", p.cmd.Process.Pid)
	// First let's try to ask the process to stop. If it's a console application, this is
	// very unlikely to work.
	var shouldJustForce bool
	// Our first attempt to gracefully close the process is taskkill. Taskkill is a windows
	// replacement for kill. However, windows does not implement signals in the same way
	// unix does, so their IPC involves "messages". Research has not shown exactly what is
	// the message sent by taskkill, but it is most likely WM_CLOSE (another option that I
	// have seen in discussions is WM_QUIT). WM_CLOSE is similar to pressing the X button on
	// an application window, which asks the process to shutdown, but the handling of this
	// "message" is up to the process. Moreover, to receive this message, a process needs to
	// have a "window". Since most viam modules do not have their own windows, killing them
	// results in a message "This process can only be terminated forcefully". However, this
	// line can potentially work if a module will have its own window.
	if out, err := exec.Command("taskkill", "/pid", pidStr).CombinedOutput(); err != nil {
		switch {
		case strings.Contains(string(out), mustForce):
			p.logger.Debug("must force terminate process")
			// if taskkill doesn't find a window to terminate the process, we will attempt to
			// send a "break" control event, which asks for a graceful shutdown of the whole
			// process group.

			// GenerateConsoleCtrlEvent functions differently from taskkill. In particular, it
			// sends a "signal" to a process group that shares a console with the calling process.
			// Since we specify the CREATE_NEW_PROCESS_GROUP flag in SysProcAttr, this pattern
			// works well for us for two reasons:
			// a) The module still shares the console with the calling process (viam-server). If
			// we were to specify CREATE_NEW_CONSOLE in the creation flags, this would no longer
			// be the case.
			// b) By creating a new process group, we are safe to send the break signal to the
			// module. If we didn't specify the CREATE_NEW_PROCESS_GROUP flag, we would risk
			// shutting down viam-server as well, since they would be in the same process group.
			if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.cmd.Process.Pid)); err != nil {
				p.logger.Debugw("sending a control break event to the process group failed with error", "err", err)
			}
			shouldJustForce = true
		case strings.Contains(string(out), "not found"):
			return false, &ProcessNotExistsError{err}
		default:
			return false, errors.Wrapf(err, "error killing process %d", p.cmd.Process.Pid)
		}
	}

	// In case the process didn't stop, or left behind any orphan children in its process group,
	// we now ask everything in the process tree to stop after a brief wait.
	timer := time.NewTimer(p.stopWaitInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		p.logger.Infof("killing entire process tree %d", p.cmd.Process.Pid)
		out, err := exec.Command("taskkill", "/t", "/pid", pidStr).CombinedOutput()
		if err != nil {
			switch {
			case strings.Contains(string(out), mustForce):
				p.logger.Debug("must force terminate process tree")
				shouldJustForce = true
			case strings.Contains(string(out), "not found"):
				return false, &ProcessNotExistsError{err}
			default:
				return false, errors.Wrapf(err, "error killing process tree %d", p.cmd.Process.Pid)
			}
		}
	case <-p.managingCh:
		timer.Stop()
		return false, nil
	}

	// Lastly, kill everything in the process tree that remains after a longer wait or now. This is
	// going to likely result in an "exit status 1" that we will have to interpret.
	// FUTURE(erd): find a way to do this better. Research has not come up with much and is
	// program dependent.

	// We can force kill the process group right away, if the flag is already set
	forceKillCommand := exec.Command("taskkill", "/t", "/f", "/pid", pidStr)
	if shouldJustForce {
		if out, err := forceKillCommand.CombinedOutput(); err != nil {
			switch {
			case strings.Contains(string(out), "not found"):
				return false, &ProcessNotExistsError{err}
			default:
				return false, errors.Wrapf(err, "error killing process %d", p.cmd.Process.Pid)
			}
		}
		return true, nil
	}

	// If shouldJustForce is not set yet, we will wait on a timer to give managing channel a
	// final chance to close. If it doesn't, we will force kill the process tree.
	timer2 := time.NewTimer(p.stopWaitInterval * 2)
	defer timer2.Stop()
	select {
	case <-timer2.C:
		p.logger.Infof("force killing entire process tree %d", p.cmd.Process.Pid)
		if out, err := forceKillCommand.CombinedOutput(); err != nil {
			switch {
			case strings.Contains(string(out), "not found"):
				return false, &ProcessNotExistsError{err}
			default:
				return false, errors.Wrapf(err, "error killing process %d", p.cmd.Process.Pid)
			}
		}
		return true, nil
	case <-p.managingCh:
		timer2.Stop()
	}
	return false, nil
}

// forceKillGroup kills everything in the process tree. This will not wait for completion and may result in a zombie process.
func (p *managedProcess) forceKillGroup() error {
	pidStr := strconv.Itoa(p.cmd.Process.Pid)
	p.logger.Infof("force killing entire process tree %d", p.cmd.Process.Pid)
	return exec.Command("taskkill", "/t", "/f", "/pid", pidStr).Start()
}

// Status is a best effort method to return an os.ErrProcessDone in case the process no
// longer exists.
func (p *managedProcess) status() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pid, err := p.UnixPid()
	if err != nil {
		return err
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	defer windows.CloseHandle(handle)
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			// Bohdan: my understanding is that Invalid_Paramater is not a strong guarantee, but
			// it's highly likely that we can treat it as "ProcessDone".
			return os.ErrProcessDone
		}
		// A common error here could be Access_Denied, which would signal that the process
		// still exists.
		return err
	}

	// To be extra sure, we can examine the exit code of the process handle.
	var exitCode uint32
	err = windows.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return err
	}
	// Somehow, this constant is not defined in the windows library, but it looks like it's
	// a commonly used Windows constant to check that the process is still running.
	const STILL_ACTIVE = 259
	if exitCode != STILL_ACTIVE {
		return os.ErrProcessDone
	}
	return nil
}
