package logging

import (
	"io"
	"sync/atomic"
)

// Logger is the minimal interface every layer uses to emit structured log
// lines. It lives in the shared kernel so modules depend on this contract
// rather than a concrete logger.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warn(format string, args ...any)
	Error(format string, args ...any)
}

// NopLogger discards every call. Used as a defensive nil-default so every
// code path that reaches for a Logger has a non-nil receiver.
type NopLogger struct{}

func (NopLogger) Debug(string, ...any) {}
func (NopLogger) Info(string, ...any)  {}
func (NopLogger) Warn(string, ...any)  {}
func (NopLogger) Error(string, ...any) {}

// LogLevel is the severity bucket for plugin logs. Mosquitto's own log_dest
// captures stderr, so the plugin emits structured lines with a stable
// [LAYER:Component] prefix that ops dashboards can grep and classify.
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

// stderrLogger is the production logger. Writes one line per call to the
// supplied writer (defaults to os.Stderr) with a millisecond timestamp +
// level. The component prefix is supplied by each call site.
//
// Concurrent-safe via a single io.Writer assumption. os.Stderr's underlying
// syscall is atomic for small writes on POSIX (PIPE_BUF = 4096 bytes), so
// log lines under 4KB are not interleaved.
type stderrLogger struct {
	w        io.Writer
	minLevel atomic.Int32
}

var (
	_ Logger = (*stderrLogger)(nil)
	_ Logger = NopLogger{}
)
