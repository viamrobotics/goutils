//go:build linux || darwin

package pexec

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"

	"go.viam.com/test"
)

func TestProcessGroupGone(t *testing.T) {
	wrapped := func(err error) error {
		return fmt.Errorf("error signaling process group 1234 with signal terminated: %w", err)
	}

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		// The group is gone entirely, which is what remains once the process has been reaped.
		{"ESRCH", syscall.ESRCH, true},
		// Darwin's answer for a group whose only member is an unreaped zombie.
		{"EPERM", syscall.EPERM, true},
		{"wrapped ESRCH", wrapped(syscall.ESRCH), true},
		{"wrapped EPERM", wrapped(syscall.EPERM), true},
		{"ErrProcessDone", os.ErrProcessDone, true},
		{"wrapped ErrProcessDone", wrapped(os.ErrProcessDone), true},
		{"unrelated errno", syscall.EACCES, false},
		{"unrelated error", errors.New("could not stop process"), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test.That(t, processGroupGone(tc.err), test.ShouldEqual, tc.want)
		})
	}
}
