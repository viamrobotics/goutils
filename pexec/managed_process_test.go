package pexec

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"

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
						"trap \"echo world >> %[1]s\nexit 0\" SIGINT; echo hello >> %[1]s\nwhile true; do echo hey; sleep 1; done",
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
	t.Run("stopping before start has no effect", func(t *testing.T) {
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
			Args: []string{"-c", fmt.Sprintf("trap \"exit 0\" SIGINT; echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name())},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		test.That(t, proc.Stop(), test.ShouldBeNil)
		test.That(t, proc.Start(context.Background()), test.ShouldEqual, errAlreadyStopped)

		proc = NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", fmt.Sprintf("trap \"exit 1\" SIGINT; echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name())},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		err = proc.Stop()
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "exit status 1")

		proc = NewManagedProcess(ProcessConfig{
			Name: "bash",
			Args: []string{"-c", fmt.Sprintf("trap \"echo woo\" SIGINT; echo hello >> %s\nwhile true; do echo hey; sleep 1; done", temp.Name())},
		}, logger)
		test.That(t, proc.Start(context.Background()), test.ShouldBeNil)

		<-watcher.Events

		err = proc.Stop()
		test.That(t, err, test.ShouldBeNil)
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
