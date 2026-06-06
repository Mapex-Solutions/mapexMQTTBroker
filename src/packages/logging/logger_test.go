package logging

import (
	"bytes"
	"strings"
	"testing"
)

// TestLogger_MinLevelSuppressesDebug confirms LogInfo level filters out the
// debug noise so production logs stay clean. Critical: a chatty plugin at
// debug-level under 16k events/s would flood log aggregators.
func TestLogger_MinLevelSuppressesDebug(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, LogInfo)
	log.Debug("this should not appear")
	log.Info("this should appear")
	if strings.Contains(buf.String(), "should not appear") {
		t.Fatalf("LogInfo level leaked DEBUG line: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "should appear") {
		t.Fatalf("LogInfo line was suppressed: %s", buf.String())
	}
}
