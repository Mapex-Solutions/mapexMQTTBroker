package broker

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// Severity bucket for plugin logs. Mosquitto's own log_dest captures
// stderr, so the plugin emits structured lines with a stable prefix
// (`[PLUGIN:Mosquitto]`) that ops dashboards can grep + classify.
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

// Logger is the minimal interface plugin code uses to emit. Decoupled
// from os.Stderr so unit tests can capture lines without touching the
// process-global stderr — see captureLogger in tests.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warn(format string, args ...any)
	Error(format string, args ...any)
}

// stderrLogger is the production logger. Writes one line per call to
// the supplied writer (defaults to os.Stderr) with a millisecond
// timestamp + level + a fixed `[PLUGIN:Mosquitto]` layer prefix so
// log scrapers can classify lines emitted by this binary.
//
// Concurrent-safe via a single io.Writer assumption. os.Stderr's
// underlying syscall is atomic for small writes on POSIX (PIPE_BUF =
// 4096 bytes), so log lines under 4KB are not interleaved.
type stderrLogger struct {
	w        io.Writer
	minLevel atomic.Int32
}

// NewLogger constructs a stderr-backed Logger that emits lines at or
// above minLevel. Defaults reasonable for production: LogInfo means
// debug noise is suppressed but flow + warnings + errors flow.
func NewLogger(w io.Writer, minLevel LogLevel) Logger {
	if w == nil {
		w = os.Stderr
	}
	l := &stderrLogger{w: w}
	l.minLevel.Store(int32(minLevel))
	return l
}

func (l *stderrLogger) write(level LogLevel, label, format string, args ...any) {
	if int32(level) < l.minLevel.Load() {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(l.w, "%s %s [PLUGIN:Mosquitto] %s\n", now, label, msg)
}

func (l *stderrLogger) Debug(format string, args ...any) {
	l.write(LogDebug, "DEBUG", format, args...)
}
func (l *stderrLogger) Info(format string, args ...any) {
	l.write(LogInfo, "INFO ", format, args...)
}
func (l *stderrLogger) Warn(format string, args ...any) {
	l.write(LogWarn, "WARN ", format, args...)
}
func (l *stderrLogger) Error(format string, args ...any) {
	l.write(LogError, "ERROR", format, args...)
}

// nopLogger discards every call. Used as a defensive default so
// every code path that reaches for a Logger has a non-nil receiver
// even before NewLogger is called.
type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// truncate returns s clipped to maxLen runes plus an ellipsis when
// overlong. Used on usernames in log lines so PII / customer
// identifiers don't bloat aggregator storage and a malicious device
// can't smuggle a 1MB username into the log pipeline.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
