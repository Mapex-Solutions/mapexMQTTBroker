package logging

import (
	"fmt"
	"io"
	"os"
	"time"
)

// New constructs a stderr-backed Logger that emits lines at or above
// minLevel. Defaults reasonable for production: LogInfo means debug noise is
// suppressed but flow + warnings + errors flow.
func New(w io.Writer, minLevel LogLevel) Logger {
	if w == nil {
		w = os.Stderr
	}
	l := &stderrLogger{w: w}
	l.minLevel.Store(int32(minLevel))
	return l
}

// Debug emits at LogDebug.
func (l *stderrLogger) Debug(format string, args ...any) {
	l.write(LogDebug, "DEBUG", format, args...)
}

// Info emits at LogInfo.
func (l *stderrLogger) Info(format string, args ...any) {
	l.write(LogInfo, "INFO ", format, args...)
}

// Warn emits at LogWarn.
func (l *stderrLogger) Warn(format string, args ...any) {
	l.write(LogWarn, "WARN ", format, args...)
}

// Error emits at LogError.
func (l *stderrLogger) Error(format string, args ...any) {
	l.write(LogError, "ERROR", format, args...)
}

// write renders one line when level passes the configured minimum. The
// component prefix is supplied by each call site via the format string.
func (l *stderrLogger) write(level LogLevel, label, format string, args ...any) {
	if int32(level) < l.minLevel.Load() {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(l.w, "%s %s %s\n", now, label, msg)
}
