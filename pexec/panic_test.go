package pexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// mockLogger captures all log calls with their levels
type mockLogger struct {
	name string
	logs []logEntry
}

type logEntry struct {
	level   string
	message string
	logger  string
}

func (m *mockLogger) Debug(args ...interface{})                 { m.log("DEBUG", args...) }
func (m *mockLogger) Debugf(template string, args ...interface{}) { m.logf("DEBUG", template, args...) }
func (m *mockLogger) Debugw(msg string, keysAndValues ...interface{}) { m.log("DEBUG", msg) }
func (m *mockLogger) Info(args ...interface{})                  { m.log("INFO", args...) }
func (m *mockLogger) Infof(template string, args ...interface{}) { m.logf("INFO", template, args...) }
func (m *mockLogger) Infow(msg string, keysAndValues ...interface{}) { m.log("INFO", msg) }
func (m *mockLogger) Warn(args ...interface{})                  { m.log("WARN", args...) }
func (m *mockLogger) Warnf(template string, args ...interface{}) { m.logf("WARN", template, args...) }
func (m *mockLogger) Warnw(msg string, keysAndValues ...interface{}) { m.log("WARN", msg) }
func (m *mockLogger) Error(args ...interface{})                 { m.log("ERROR", args...) }
func (m *mockLogger) Errorf(template string, args ...interface{}) { m.logf("ERROR", template, args...) }
func (m *mockLogger) Errorw(msg string, keysAndValues ...interface{}) { m.log("ERROR", msg) }
func (m *mockLogger) Fatal(args ...interface{})                 { m.log("FATAL", args...) }
func (m *mockLogger) Fatalf(template string, args ...interface{}) { m.logf("FATAL", template, args...) }
func (m *mockLogger) Fatalw(msg string, keysAndValues ...interface{}) { m.log("FATAL", msg) }

func (m *mockLogger) log(level string, args ...interface{}) {
	message := fmt.Sprint(args...)
	m.logs = append(m.logs, logEntry{level: level, message: message, logger: m.name})
	fmt.Printf("[MOCK %s] %s: %s\n", m.name, level, message)
}

func (m *mockLogger) logf(level string, template string, args ...interface{}) {
	message := fmt.Sprintf(template, args...)
	m.logs = append(m.logs, logEntry{level: level, message: message, logger: m.name})
	fmt.Printf("[MOCK %s] %s: %s\n", m.name, level, message)
}

// Implement other required methods to satisfy ZapCompatibleLogger interface
func (m *mockLogger) Desugar() *zap.Logger               { return nil }
func (m *mockLogger) Level() zapcore.Level               { return zapcore.InfoLevel }
func (m *mockLogger) Named(name string) *zap.SugaredLogger {
	// Return a dummy sugared logger, but our mock will still capture calls
	return zap.NewNop().Sugar()
}
func (m *mockLogger) Sync() error                        { return nil }
func (m *mockLogger) WithOptions(opts ...zap.Option) *zap.SugaredLogger {
	return zap.NewNop().Sugar()
}

func TestPexecPanicLogging(t *testing.T) {
	// Create a temporary Go program that panics
	tempDir := t.TempDir()
	panicProgram := filepath.Join(tempDir, "main.go")

	panicCode := `package main

func main() {
	panic("test panic for pexec analysis")
}
`

	err := os.WriteFile(panicProgram, []byte(panicCode), 0644)
	if err != nil {
		t.Fatalf("Failed to write panic program: %v", err)
	}

	// Create go.mod
	goMod := filepath.Join(tempDir, "go.mod")
	err = os.WriteFile(goMod, []byte("module panictest\ngo 1.21\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to write go.mod: %v", err)
	}

	// Create mock loggers to capture exactly what gets logged
	mainLogger := &mockLogger{name: "main"}
	stdoutLogger := &mockLogger{name: "stdout"}
	stderrLogger := &mockLogger{name: "stderr"}

	// Configure ProcessConfig
	config := ProcessConfig{
		ID:           "panic-test",
		Name:         "go",
		Args:         []string{"run", "main.go"},
		CWD:          tempDir,
		Log:          true,
		StdOutLogger: stdoutLogger,
		StdErrLogger: stderrLogger,
	}
	//start processes
	process := NewManagedProcess(config, mainLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = process.Start(ctx)
	if err != nil {
		fmt.Printf("Process start error (expected): %v\n", err)
	}

	// Wait a moment for the process to run and panic
	time.Sleep(5 * time.Second)
	process.Stop()

	// Output the captured logs
	fmt.Println("=== MAIN LOGGER ===")
	for _, log := range mainLogger.logs {
		fmt.Printf("  [%s] %s: %s\n", log.logger, log.level, log.message)
	}
	fmt.Println("=== STDOUT LOGGER ===")
	for _, log := range stdoutLogger.logs {
		fmt.Printf("  [%s] %s: %s\n", log.logger, log.level, log.message)
	}
	fmt.Println("=== STDERR LOGGER ===")
	for _, log := range stderrLogger.logs {
		fmt.Printf("  [%s] %s: %s\n", log.logger, log.level, log.message)
	}

	// fmt.Println("\n=== ANALYSIS ===")
	// fmt.Printf("Main logger captured %d logs:\n", len(mainLogger.logs))
	// for _, log := range mainLogger.logs {
	// 	fmt.Printf("  [%s] %s: %s\n", log.logger, log.level, log.message)
	// }

	// fmt.Printf("\nStdOut logger captured %d logs:\n", len(stdoutLogger.logs))
	// for _, log := range stdoutLogger.logs {
	// 	fmt.Printf("  [%s] %s: %s\n", log.logger, log.level, log.message)
	// 	if strings.Contains(log.message, "panic") {
	// 		fmt.Printf("    ^^^ PANIC FOUND IN STDOUT! ^^^\n")
	// 	}
	// }

	// fmt.Printf("\nStdErr logger captured %d logs:\n", len(stderrLogger.logs))
	// for _, log := range stderrLogger.logs {
	// 	fmt.Printf("  [%s] %s: %s\n", log.logger, log.level, log.message)
	// 	if strings.Contains(log.message, "panic") {
	// 		fmt.Printf("    ^^^ PANIC FOUND IN STDERR! ^^^\n")
	// 	}
	// }

	// // Check if panic appears in both streams
	// panicInStdout := false
	// panicInStderr := false

	// for _, log := range stdoutLogger.logs {
	// 	if strings.Contains(log.message, "panic") {
	// 		panicInStdout = true
	// 		fmt.Printf("\n🚨 STDOUT Logger.%s() called with panic!\n", log.level)
	// 	}
	// }

	// for _, log := range stderrLogger.logs {
	// 	if strings.Contains(log.message, "panic") {
	// 		panicInStderr = true
	// 		fmt.Printf("\n🚨 STDERR Logger.%s() called with panic!\n", log.level)
	// 	}
	// }

	// fmt.Printf("\n=== SUMMARY ===\n")
	// fmt.Printf("Panic found in stdout logger: %v\n", panicInStdout)
	// fmt.Printf("Panic found in stderr logger: %v\n", panicInStderr)

	// if panicInStdout && panicInStderr {
	// 	fmt.Printf("🔥 DOUBLE LOGGING CONFIRMED: Panic went through BOTH stdout and stderr!\n")
	// } else if panicInStdout {
	// 	fmt.Printf("🤔 UNEXPECTED: Panic only went through stdout\n")
	// } else if panicInStderr {
	// 	fmt.Printf("✅ EXPECTED: Panic only went through stderr\n")
	// } else {
	// 	fmt.Printf("❓ NO PANIC CAPTURED: Something went wrong\n")
	// }
}