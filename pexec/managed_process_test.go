package pexec

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"go.viam.com/test"

	"go.viam.com/utils"
)

func TestManagedProcessID(t *testing.T) {
	logger := golog.NewTestLogger(t)
	p1 := NewManagedProcess(ProcessConfig{
		ID:      "1",
		Args:    []string{"-c", "echo hello"},
		OneShot: true,
	}, logger)
	p2 := NewManagedProcess(ProcessConfig{
		ID:      "2",
		Name:    "bash",
		Args:    []string{"-cb", "echo hello"},
		OneShot: true,
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
			}, logger)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := proc.Start(ctx)
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, errors.Is(err, context.Canceled), test.ShouldBeTrue)
		})
		t.Run("starting with an eventually canceled context should fail", func(t *testing.T) {
			logger := golog.NewTestLogger(t)
			temp, err := os.CreateTemp("", "*.txt")
			test.That(t, err, test.ShouldBeNil)
			defer os.Remove(temp.Name())

			watcher, err := fsnotify.NewWatcher()
			test.That(t, err, test.ShouldBeNil)
			defer watcher.Close()
			watcher.Add(temp.Name())

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				<-watcher.Events
				cancel()
			}()

			proc := NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", fmt.Sprintf("echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name())},
				OneShot: true,
			}, logger)
			err = proc.Start(ctx)
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "killed")
		})
		t.Run("starting with a normal context", func(t *testing.T) {
			logger := golog.NewTestLogger(t)

			temp, err := os.CreateTemp("", "*.txt")
			test.That(t, err, test.ShouldBeNil)
			defer os.Remove(temp.Name())
			proc := NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", fmt.Sprintf(`echo hello >> %s`, temp.Name())},
				OneShot: true,
			}, logger)
			test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

			rd, err := os.ReadFile(temp.Name())
			test.That(t, err, test.ShouldBeNil)
			test.That(t, string(rd), test.ShouldEqual, "hello\n")

			proc = NewManagedProcess(ProcessConfig{
				Name:    "bash",
				Args:    []string{"-c", "exit 1"},
				OneShot: true,
			}, logger)
			err = proc.Start(context.Background())
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
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

			temp, err := os.CreateTemp("", "*.txt")
			test.That(t, err, test.ShouldBeNil)
			defer os.Remove(temp.Name())

			watcher, err := fsnotify.NewWatcher()
			test.That(t, err, test.ShouldBeNil)
			defer watcher.Close()
			watcher.Add(temp.Name())

			proc := NewManagedProcess(ProcessConfig{
				Name: "bash",
				Args: []string{
					"-c",
					fmt.Sprintf(
						"trap \"echo world >> %[1]s\nexit 0\" SIGTERM; echo hello >> %[1]s\nwhile true; do echo hey; sleep 1; done",
						temp.Name(),
					),
				},
			}, logger)
			test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

			<-watcher.Events

			test.That(t, proc.Stop(), test.ShouldBeNil)

			rd, err := os.ReadFile(temp.Name())
			test.That(t, err, test.ShouldBeNil)
			test.That(t, string(rd), test.ShouldEqual, "hello\nworld\n")
		})
	})
}

func TestManagedProcessManage(t *testing.T) {
	t.Run("a managed process that dies should be restarted", func(t *testing.T) {
		logger := golog.NewTestLogger(t)

		temp, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp.Name())

		watcher, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher.Close()
		watcher.Add(temp.Name())

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", fmt.Sprintf("echo hello >> %s\nexit 1", temp.Name())},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events
		<-watcher.Events
		<-watcher.Events

		err = proc.Stop()
		// sometimes we simply cannot get the status
		if err != nil {
			test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")
		}
	})
}

func TestManagedProcessStop(t *testing.T) {
	t.Parallel()
	t.Run("stopping before start has no effect", func(t *testing.T) {
		t.Parallel()
		logger := golog.NewTestLogger(t)
		proc := NewManagedProcess(ProcessConfig{
			Name:    "bash",
			Args:    []string{"-c", "echo hello"},
			OneShot: true,
		}, logger)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)
	})
	t.Run("stopping a one shot does nothing", func(t *testing.T) {
		t.Parallel()
		logger := golog.NewTestLogger(t)
		proc := NewManagedProcess(ProcessConfig{
			Name:    "bash",
			Args:    []string{"-c", "echo hello"},
			OneShot: true,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)
	})
	t.Run("stopping a managed process gives it a chance to finish", func(t *testing.T) {
		t.Parallel()
		logger := golog.NewTestLogger(t)

		temp, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp.Name())

		watcher, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher.Close()
		watcher.Add(temp.Name())

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("trap \"exit 0\" SIGTERM; echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name()),
			},
			StopTimeout: time.Second * 5,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)

		proc = NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("trap \"exit 1\" SIGTERM; echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name()),
			},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		err = proc.Stop()
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")

		proc = NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("trap \"echo woo\" SIGTERM; echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name()),
			},
			StopTimeout: time.Second * 3,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		err = proc.Stop()
		test.That(t, err, test.ShouldBeNil)
	})
	t.Run("stop signal selection", func(t *testing.T) {
		t.Parallel()
		logger := golog.NewTestLogger(t)

		temp, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp.Name())

		watcher, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher.Close()
		watcher.Add(temp.Name())

		bashScript := fmt.Sprintf(`
			trap "exit %d" SIGHUP
			trap "exit %d" SIGINT
			trap "exit %d" SIGQUIT
			trap "exit %d" SIGABRT
			trap "exit %d" SIGUSR1
			trap "exit %d" SIGUSR2
			trap "exit %d" SIGTERM
			echo hello >> %s
			while true
			do echo hey
			sleep 1
			done
		`,
			100+syscall.SIGHUP,
			100+syscall.SIGINT,
			100+syscall.SIGQUIT,
			100+syscall.SIGABRT,
			100+syscall.SIGUSR1,
			100+syscall.SIGUSR2,
			100+syscall.SIGTERM,
			temp.Name(),
		)

		proc := NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", bashScript},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		<-watcher.Events
		err = proc.Stop()
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 115")

		for _, signal := range []syscall.Signal{
			syscall.SIGHUP,
			syscall.SIGINT,
			syscall.SIGQUIT,
			syscall.SIGABRT,
			syscall.SIGUSR1,
			syscall.SIGUSR2,
			syscall.SIGTERM,
		} {
			t.Run(fmt.Sprintf("sig=%d", signal), func(t *testing.T) {
				proc = NewManagedProcess(ProcessConfig{
					Name:       "bash",
					Args:       []string{"-c", bashScript},
					StopSignal: signal,
				}, logger)
				test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
				<-watcher.Events
				err = proc.Stop()
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldContainSubstring, fmt.Sprintf("exit status %d", signal+100))
			})
		}
	})
	t.Run("stop wait signaling", func(t *testing.T) {
		t.Parallel()
		logger := golog.NewTestLogger(t)

		temp1, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp1.Name())

		watcher1, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher1.Close()
		watcher1.Add(temp1.Name())

		bashScript1 := fmt.Sprintf(`
			trap "echo SIGTERM >> %s" SIGTERM
			echo hello >> %s
			while true
			do echo hey
			sleep 1
			done
		`, temp1.Name(), temp1.Name())

		proc := NewManagedProcess(ProcessConfig{
			Name:        "bash",
			Args:        []string{"-c", bashScript1},
			StopTimeout: time.Second * 5,
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)
		<-watcher1.Events
		test.That(t, proc.Stop(), test.ShouldBeNil)

		reader1 := bufio.NewReader(temp1)
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
		t.Parallel()
		logger := golog.NewTestLogger(t)

		temp1, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp1.Name())

		temp2, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp2.Name())

		temp3, err := os.CreateTemp("", "*.txt")
		test.That(t, err, test.ShouldBeNil)
		defer os.Remove(temp3.Name())

		watcher1, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher1.Close()
		watcher1.Add(temp1.Name())

		watcher2, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher2.Close()
		watcher2.Add(temp1.Name())

		watcher3, err := fsnotify.NewWatcher()
		test.That(t, err, test.ShouldBeNil)
		defer watcher3.Close()
		watcher3.Add(temp3.Name())

		trapScript := `
			trap "echo SIGTERM >> %s" SIGTERM
			echo hello >> %s
			while true
			do echo hey
			sleep 1
			done
		`

		bashScript1 := fmt.Sprintf(trapScript, temp1.Name(), temp1.Name())
		bashScript2 := fmt.Sprintf(trapScript, temp2.Name(), temp2.Name())
		bashScriptParent := fmt.Sprintf(`
			bash -c '%s' &
			bash -c '%s' &
			`+trapScript,
			bashScript1,
			bashScript2,
			temp3.Name(),
			temp3.Name(),
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
		test.That(t, countTerms(temp1), test.ShouldEqual, 1)
		test.That(t, countTerms(temp2), test.ShouldEqual, 1)
		test.That(t, countTerms(temp3), test.ShouldEqual, 2)
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
