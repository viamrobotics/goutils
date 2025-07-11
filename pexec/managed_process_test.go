package pexec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.viam.com/test"

	"go.viam.com/utils"
	"go.viam.com/utils/testutils"
)

// User for subprocess tests.
// This looks for TEST_SUBPROC_USER var, otherwise uses current user.
func subprocUser() (*user.User, error) {
	if usernameFromEnv := os.Getenv("TEST_SUBPROC_USER"); len(usernameFromEnv) > 0 {
		return user.Lookup(usernameFromEnv)
	}
	return user.Current()
}

func TestManagedProcessID(t *testing.T) {
	logger := golog.NewTestLogger(t)
	p1 := NewManagedProcess(ProcessConfig{
		ID:      "1",
		Args:    []string{"-c", "echo hello"},
		OneShot: true,
		Log:     true,
	}, logger)
	p2 := NewManagedProcess(ProcessConfig{
		ID:      "2",
		Name:    "bash",
		Args:    []string{"-cb", "echo hello"},
		OneShot: true,
		Log:     true,
	}, logger)
	test.That(t, p1.ID(), test.ShouldEqual, "1")
	test.That(t, p2.ID(), test.ShouldEqual, "2")
}

func TestManagedProcessStart(t *testing.T) {
	t.Run("OneShot", func(t *testing.T) {
		t.Run("starting with a canceled context should fail", func(t *testing.T) {
			logger := golog.NewTestLogger(t)
			proc := NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", "echo hello"},
				OneShot: true,
				Log:     true,
			}, logger)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := proc.Start(ctx)
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, errors.Is(err, context.Canceled), test.ShouldBeTrue)
		})
		t.Run("starting with an eventually canceled context should fail", func(t *testing.T) {
			logger := golog.NewTestLogger(t)

			watcher, tempFile := testutils.WatchedFile(t)

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				<-watcher.Events
				cancel()
			}()

			proc := NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", fmt.Sprintf("echo hello >> '%s'\nwhile true; do echo hey; sleep 1; done", tempFile.Name())},
				OneShot: true,
				Log:     true,
			}, logger)
			err := proc.Start(ctx)
			test.That(t, err, test.ShouldNotBeNil)
			if runtime.GOOS == "windows" {
				test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
			} else {
				test.That(t, err.Error(), test.ShouldContainSubstring, "killed")
			}
		})
		t.Run("starting with a normal context", func(t *testing.T) {
			logger := golog.NewTestLogger(t)

			tempFile := testutils.TempFile(t)

			proc := NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", fmt.Sprintf(`echo hello >> '%s'`, tempFile.Name())},
				OneShot: true,
				Log:     true,
			}, logger)
			test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

			rd, err := os.ReadFile(tempFile.Name())
			test.That(t, err, test.ShouldBeNil)
			test.That(t, string(rd), test.ShouldEqual, "hello\n")

			proc = NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", "exit 1"},
				OneShot: true,
				Log:     true,
			}, logger)
			err = proc.Start(context.Background())
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
		})
		t.Run("OnUnexpectedExit is ignored", func(t *testing.T) {
			logger := golog.NewTestLogger(t)
			proc := NewManagedProcess(ProcessConfig{
				Name:             "bash",
				Args:             []string{"-c", "exit 1"},
				OneShot:          true,
				Log:              true,
				OnUnexpectedExit: func(int) bool { panic("this should not panic") },
			}, logger)
			err := proc.Start(context.Background())
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
		})
		t.Run("providing a nonexistent cwd should fail", func(t *testing.T) {
			logger := golog.NewTestLogger(t)
			proc := NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", "echo hello"},
				OneShot: true,
				Log:     true,
				CWD:     "idontexist",
			}, logger)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := proc.Start(ctx)
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, `error setting process working directory to "idontexist"`)
		})
		t.Run("resolves relative path with different cwd", func(t *testing.T) {
			logger := golog.NewTestLogger(t)
			newWD := t.TempDir()
			// create an executable file in there that we will refence as a just "./exec.sh"
			executablePath := filepath.Join(newWD, "exec.sh")
			err := os.WriteFile(executablePath, []byte("#!/bin/sh\necho hi\n"), 0o700)
			test.That(t, err, test.ShouldBeNil)

			logReader, logWriter := io.Pipe()
			proc := NewManagedProcess(ProcessConfig{
				Name:      "./exec.sh",
				OneShot:   true,
				Log:       true,
				CWD:       newWD,
				LogWriter: logWriter,
			}, logger)

			var activeReaders sync.WaitGroup
			activeReaders.Add(1)
			utils.PanicCapturingGo(func() {
				defer activeReaders.Done()
				bufferedLogReader := bufio.NewReader(logReader)
				line, err := bufferedLogReader.ReadString('\n')
				test.That(t, err, test.ShouldBeNil)
				test.That(t, line, test.ShouldEqual, "hi\n")
			})
			err = proc.Start(context.Background())
			test.That(t, err, test.ShouldBeNil)
			activeReaders.Wait()
		})
	})
	t.Run("Managed", func(t *testing.T) {
		t.Run("starting with a canceled context should have no effect", func(t *testing.T) {
			logger := golog.NewTestLogger(t)
			proc := NewManagedProcess(ProcessConfig{
				Name: "bash",
				Args: []string{"-c", "echo hello"},
			}, logger)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			test.That(t, proc.Start(ctx), test.ShouldBeNil)
			test.That(t, proc.Stop(), test.ShouldBeNil)
		})
		t.Run("starting with a normal context should run until stop", func(t *testing.T) {
			logger := golog.NewTestLogger(t)

			watcher, tempFile := testutils.WatchedFile(t)

			proc := NewManagedProcess(ProcessConfig{
				Name: "bash",
				Args: []string{
					"-c",
					fmt.Sprintf(
						"trap \"echo world >> '%[1]s'\nexit 0\" SIGTERM; echo hello >> '%[1]s'\nwhile true; do echo hey; sleep 1; done",
						tempFile.Name(),
					),
				},
			}, logger)
			test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

			<-watcher.Events

			test.That(t, proc.Status(), test.ShouldBeNil)
			test.That(t, proc.Stop(), test.ShouldBeNil)
			test.That(t, proc.Status().Error(), test.ShouldContainSubstring, "process already finished")

			rd, err := os.ReadFile(tempFile.Name())
			test.That(t, err, test.ShouldBeNil)
			if runtime.GOOS == "windows" {
				test.That(t, string(rd), test.ShouldEqual, "hello\n")
			} else {
				test.That(t, string(rd), test.ShouldEqual, "hello\nworld\n")
			}
		})
		t.Run("run as user", func(t *testing.T) {
			if curUser, _ := user.Current(); curUser.Username != "root" {
				t.Skipf("skipping run-as-user because setuid required elevated privileges")
				return
			}
			asUser, err := subprocUser()
			if err != nil {
				t.Error(err)
				return
			}
			proc := NewManagedProcess(ProcessConfig{
				ID:       "3",
				Name:     "sleep",
				Args:     []string{"100"},
				Username: asUser.Username,
				Log:      true,
			}, golog.NewTestLogger(t))
			test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
			detectedUID, _ := exec.Command("ps", "--no-headers", "-o", "uid", "-p", strconv.Itoa(proc.(*managedProcess).cmd.Process.Pid)).Output()
			test.That(t, asUser.Uid, test.ShouldEqual, strings.Trim(string(detectedUID), " \n\r"))
			test.That(t, proc.Stop(), test.ShouldBeNil)
		})
	})
}

func TestManagedProcessManage(t *testing.T) {
	t.Run("a managed process that dies should be restarted", func(t *testing.T) {
		logger := golog.NewTestLogger(t)

		watcher, tempFile := testutils.WatchedFile(t)

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", fmt.Sprintf("echo hello >> '%s'\nexit 1", tempFile.Name())},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events
		<-watcher.Events
		<-watcher.Events

		err := proc.Stop()
		// sometimes we simply cannot get the status
		if err != nil {
			test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
		}
	})
	t.Run("OnUnexpectedExit", func(t *testing.T) {
		logger := golog.NewTestLogger(t)

		onUnexpectedExitCalledEnough := make(chan struct{})
		var (
			onUnexpectedExitCallCount atomic.Uint64
			receivedExitCode          atomic.Int64
		)
		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", "exit 1"},
			OnUnexpectedExit: func(exitCode int) bool {
				receivedExitCode.Store(int64(exitCode))

				// Close channel and return false (no restart) after 5 restarts.
				// Further calls to this function will cause a double close panic, so
				// we can be sure function is called only 5 times.
				if onUnexpectedExitCallCount.Add(1) >= 5 {
					close(onUnexpectedExitCalledEnough)
					return false
				}
				return true
			},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-onUnexpectedExitCalledEnough
		test.That(t, onUnexpectedExitCallCount.Load(), test.ShouldEqual, 5)
		// Assert that last received exit code was 1 from 'exit 1'.
		test.That(t, receivedExitCode.Load(), test.ShouldEqual, 1)

		err := proc.Stop()
		// sometimes we simply cannot get the status
		if err != nil {
			test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
		}
	})
}

func TestManagedProcessStop(t *testing.T) {
	t.Run("stopping before start has no effect", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		proc := NewManagedProcess(ProcessConfig{
			Name:    "bash",
			Args:    []string{"-c", "echo hello"},
			OneShot: true,
			Log:     true,
		}, logger)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)
	})
	t.Run("stopping a one shot does nothing", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		proc := NewManagedProcess(ProcessConfig{
			Name:    "bash",
			Args:    []string{"-c", "echo hello"},
			OneShot: true,
			Log:     true,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)
	})
	t.Run("stopping a managed process gives it a chance to finish", func(t *testing.T) {
		logger := golog.NewTestLogger(t)

		watcher, tempFile := testutils.WatchedFile(t)

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("trap \"exit 0\" SIGTERM; echo hello >> '%s'\nwhile true; do echo hey; sleep 1; done", tempFile.Name()),
			},
			StopTimeout: time.Second * 5,
			Log:         true,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		time.Sleep(2 * time.Second)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)

		proc = NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("trap \"exit 1\" SIGTERM; echo hello >> '%s'\nwhile true; do echo hey; sleep 1; done", tempFile.Name()),
			},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		test.That(t, proc.Status(), test.ShouldBeNil)
		err := proc.Stop()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, proc.Status(), test.ShouldNotBeNil)

		proc = NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("trap \"echo woo\" SIGTERM; echo hello >> '%s'\nwhile true; do echo hey; sleep 1; done", tempFile.Name()),
			},
			StopTimeout: time.Second * 3,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		err = proc.Stop()
		test.That(t, err, test.ShouldBeNil)
	})
	t.Run("stop signal selection", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("cannot test this on windows")
		}
		logger := golog.NewTestLogger(t)

		watcher, tempFile := testutils.WatchedFile(t)

		var bashScriptBuilder strings.Builder
		for _, sig := range knownSignals {
			bashScriptBuilder.WriteString(
				fmt.Sprintf(`trap "exit %d" %s`,
					100+sig, sigStr(sig),
				))
			bashScriptBuilder.WriteString("\n")
		}
		bashScriptBuilder.WriteString(fmt.Sprintf(`echo hello >> '%s'
	while true
	do echo hey
	sleep 1
	done`, tempFile.Name()))
		bashScriptBuilder.WriteString("\n")

		bashScript := bashScriptBuilder.String()

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", bashScript},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		<-watcher.Events
		test.That(t, proc.Status(), test.ShouldBeNil)
		err := proc.Stop()
		test.That(t, err, test.ShouldBeNil)
		test.That(t, proc.Status(), test.ShouldNotBeNil)

		for _, signal := range knownSignals {
			t.Run(fmt.Sprintf("sig=%s", sigStr(signal)), func(t *testing.T) {
				proc = NewManagedProcess(ProcessConfig{
					Name:       "bash",
					Args:       []string{"-c", bashScript},
					StopSignal: signal,
				}, logger)
				test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
				<-watcher.Events
				err = proc.Stop()
				test.That(t, err, test.ShouldBeNil)
			})
		}
	})
	t.Run("stop wait signaling", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("cannot test this on windows")
		}
		logger := golog.NewTestLogger(t)

		watcher1, tempFile := testutils.WatchedFile(t)

		bashScript1 := fmt.Sprintf(`
			trap "echo SIGTERM >> '%s'" SIGTERM
			echo hello >> '%s'
			while true
			do echo hey
			sleep 1
			done
		`, tempFile.Name(), tempFile.Name())

		proc := NewManagedProcess(ProcessConfig{
			Name:        "bash",
			Args:        []string{"-c", bashScript1},
			StopTimeout: time.Second * 5,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		<-watcher1.Events
		test.That(t, proc.Stop(), test.ShouldBeNil)

		reader1 := bufio.NewReader(tempFile)
		var numSignals uint
		for {
			line, err := reader1.ReadString('\n')
			if err != nil {
				break
			}
			if strings.Contains(line, "SIGTERM") {
				numSignals++
			}
		}
		test.That(t, numSignals, test.ShouldEqual, 2)
	})
	t.Run("stop wait signaling with children", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("cannot test this on windows")
		}
		logger := golog.NewTestLogger(t)

		watcher1, tempFile1 := testutils.WatchedFile(t)
		watcher2, tempFile2 := testutils.WatchedFile(t)
		watcher3, tempFile3 := testutils.WatchedFile(t)

		trapScript := `
			trap "echo SIGTERM >> '%s'" SIGTERM
			echo hello >> '%s'
			while true
			do echo hey
			sleep 1
			done
		`

		bashScript1 := fmt.Sprintf(trapScript, tempFile1.Name(), tempFile1.Name())
		bashScript2 := fmt.Sprintf(trapScript, tempFile2.Name(), tempFile2.Name())
		bashScriptParent := fmt.Sprintf(`
			bash -c '%s' &
			bash -c '%s' &
			`+trapScript,
			bashScript1,
			bashScript2,
			tempFile3.Name(),
			tempFile3.Name(),
		)

		proc := NewManagedProcess(ProcessConfig{
			Name:        "bash",
			Args:        []string{"-c", bashScriptParent},
			StopTimeout: time.Second * 5,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		<-watcher1.Events
		<-watcher2.Events
		<-watcher3.Events
		test.That(t, proc.Stop(), test.ShouldBeNil)

		countTerms := func(file *os.File) uint {
			reader := bufio.NewReader(file)
			var numSignals uint
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				if strings.Contains(line, "SIGTERM") {
					numSignals++
				}
			}
			return numSignals
		}

		// children should each get signaled only once, on the second stage
		// where its assumed the parent has failed to pass/signal them in stage one
		test.That(t, countTerms(tempFile1), test.ShouldEqual, 1)
		test.That(t, countTerms(tempFile2), test.ShouldEqual, 1)
		test.That(t, countTerms(tempFile3), test.ShouldEqual, 2)
	})
	t.Run("stop does not call OnUnexpectedExit", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("cannot test this on windows")
		}
		logger := golog.NewTestLogger(t)

		watcher1, tempFile := testutils.WatchedFile(t)

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("while true; do echo hello >> '%s'; sleep 1; done", tempFile.Name()),
			},
			StopTimeout:      time.Second * 5,
			OnUnexpectedExit: func(int) bool { panic("this should not panic") },
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		<-watcher1.Events
		test.That(t, proc.Stop(), test.ShouldBeNil)
	})
}

func TestManagedProcessKillGroup(t *testing.T) {
	t.Run("kill signaling with children", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("cannot test this on windows")
		}
		logger := golog.NewTestLogger(t)

		watcher1, tempFile1 := testutils.WatchedFile(t)
		watcher2, tempFile2 := testutils.WatchedFile(t)
		watcher3, tempFile3 := testutils.WatchedFile(t)

		// this script writes a string to the specified file every 100ms
		// stop after 10000 iterations (1000s or ~16m), so processes don't stay around forever if kill doesn't work
		script := `
		while [ $(( ( i += 1 ) <= 10000 )) -ne 0 ]; 
		do echo hello >> '%s'
		sleep 0.1
		done
	`

		bashScript1 := fmt.Sprintf(script, tempFile1.Name())
		bashScript2 := fmt.Sprintf(script, tempFile2.Name())
		bashScriptParent := fmt.Sprintf(`
		bash -c '%s' &
		bash -c '%s' &
		`+script,
			bashScript1,
			bashScript2,
			tempFile3.Name(),
			tempFile3.Name(),
		)

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", bashScriptParent},
		}, logger)

		// To confirm that the processes have died, confirm that the size of the file stopped increasing
		getSize := func(file *os.File) int64 {
			info, _ := file.Stat()
			return info.Size()
		}

		file1SizeBeforeStart := getSize(tempFile1)
		file2SizeBeforeStart := getSize(tempFile2)
		file3SizeBeforeStart := getSize(tempFile3)

		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher1.Events
		<-watcher2.Events
		<-watcher3.Events

		proc.KillGroup()

		file1SizeAfterKill := getSize(tempFile1)
		file2SizeAfterKill := getSize(tempFile2)
		file3SizeAfterKill := getSize(tempFile3)

		test.That(t, file1SizeAfterKill, test.ShouldBeGreaterThan, file1SizeBeforeStart)
		test.That(t, file2SizeAfterKill, test.ShouldBeGreaterThan, file2SizeBeforeStart)
		test.That(t, file3SizeAfterKill, test.ShouldBeGreaterThan, file3SizeBeforeStart)

		// wait longer than the 0.1s sleep in the child process
		time.Sleep(1 * time.Second)

		// since KillGroup does not wait, we might have to check file size a few times as the kill
		// might take a little to propagate. We want to make sure that the file size stops increasing.
		testutils.WaitForAssertionWithSleep(t, 300*time.Millisecond, 50, func(tb testing.TB) {
			tempSize1 := getSize(tempFile1)
			tempSize2 := getSize(tempFile2)
			tempSize3 := getSize(tempFile3)

			test.That(t, tempSize1, test.ShouldEqual, file1SizeAfterKill)
			test.That(t, tempSize2, test.ShouldEqual, file2SizeAfterKill)
			test.That(t, tempSize3, test.ShouldEqual, file3SizeAfterKill)

			file1SizeAfterKill = tempSize1
			file2SizeAfterKill = tempSize1
			file3SizeAfterKill = tempSize1
		})

		// wait on the managingCh to close
		<-proc.(*managedProcess).managingCh
	})
}

func TestManagedProcessEnvironmentVariables(t *testing.T) {
	t.Run("set an environment variable on one-shot process", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		output := new(bytes.Buffer)
		proc := NewManagedProcess(ProcessConfig{
			Name:        "bash",
			Args:        []string{"-c", "printenv VIAM_HOME"},
			Environment: map[string]string{"VIAM_HOME": "/opt/viam"},
			OneShot:     true,
			LogWriter:   output,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		test.That(t, output.String(), test.ShouldEqual, "/opt/viam\n")
	})

	t.Run("set an environment variable on non one-shot process", func(t *testing.T) {
		logReader, logWriter := io.Pipe()
		logger := golog.NewTestLogger(t)
		proc := NewManagedProcess(ProcessConfig{
			Name:        "bash",
			Args:        []string{"-c", "printenv VIAM_HOME"},
			Environment: map[string]string{"VIAM_HOME": "/opt/viam"},
			LogWriter:   logWriter,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		bufferedLogReader := bufio.NewReader(logReader)
		output, err := bufferedLogReader.ReadString('\n')
		test.That(t, err, test.ShouldBeNil)
		test.That(t, output, test.ShouldEqual, "/opt/viam\n")
		test.That(t, proc.Stop(), test.ShouldBeNil)
	})

	t.Run("overwrite an environment variable", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		// test that the variable already exists
		test.That(t, os.Getenv("HOME"), test.ShouldNotBeEmpty)
		output := new(bytes.Buffer)
		proc := NewManagedProcess(ProcessConfig{
			Name:        "bash",
			Args:        []string{"-c", "printenv HOME"},
			Environment: map[string]string{"HOME": "/some/dir"},
			OneShot:     true,
			LogWriter:   output,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		test.That(t, output.String(), test.ShouldEqual, "/some/dir\n")
	})
}

func TestManagedProcessLogWriter(t *testing.T) {
	t.Run("Extract output of a one shot", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		logReader, logWriter := io.Pipe()
		proc := NewManagedProcess(ProcessConfig{
			Name:      "bash",
			Args:      []string{"-c", "echo hello"},
			OneShot:   true,
			LogWriter: logWriter,
		}, logger)
		var activeReaders sync.WaitGroup
		activeReaders.Add(1)
		utils.PanicCapturingGo(func() {
			defer activeReaders.Done()
			bufferedLogReader := bufio.NewReader(logReader)
			line, err := bufferedLogReader.ReadString('\n')
			test.That(t, err, test.ShouldBeNil)
			test.That(t, line, test.ShouldEqual, "hello\n")
		})
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		activeReaders.Wait()
		test.That(t, proc.Stop(), test.ShouldBeNil)
	})

	t.Run("Get log lines from a process", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		logReader, logWriter := io.Pipe()
		proc := NewManagedProcess(ProcessConfig{
			Name:      "bash",
			Args:      []string{"-c", "echo hello"},
			LogWriter: logWriter,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		bufferedLogReader := bufio.NewReader(logReader)
		for i := 0; i < 5; i++ {
			line, err := bufferedLogReader.ReadString('\n')
			test.That(t, err, test.ShouldBeNil)
			test.That(t, line, test.ShouldEqual, "hello\n")
		}
		test.That(t, proc.Stop(), test.ShouldBeNil)
	})
}

type fakeProcess struct {
	id        string
	stopCount int
	startErr  bool
	stopErr   bool
}

func (fp *fakeProcess) ID() string {
	return fp.id
}

func (fp *fakeProcess) Start(ctx context.Context) error {
	if fp.startErr {
		return errors.New("start")
	}
	return nil
}

func (fp *fakeProcess) Stop() error {
	fp.stopCount++
	if fp.stopErr {
		return errors.New("stop")
	}
	return nil
}

func (fp *fakeProcess) Status() error {
	if fp.stopErr || fp.startErr {
		return errors.New("dead")
	}
	return nil
}

func (fp *fakeProcess) UnixPid() (int, error) {
	return 0, errors.New(`the NewManagedProcess API needlessly returns an interface
 instead of the structure itself. Thus tests depend on the returned interface. When
in reality tests should just depend on the methods they rely on. UnixPid is not one
of those methods (for better or worse)`)
}

func (fp *fakeProcess) KillGroup() {}
