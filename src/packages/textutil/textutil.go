// Package textutil holds tiny, dependency-free string helpers shared
// across every layer. It imports nothing from the broker tree, so any
// layer may depend on it without creating an inward dependency violation.
package textutil

// Truncate returns s clipped to maxLen bytes plus an ellipsis when
// overlong. Used on usernames and identifiers in log lines so PII does
// not bloat aggregator storage and a malicious device cannot smuggle a
// 1MB username into the log pipeline.
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
