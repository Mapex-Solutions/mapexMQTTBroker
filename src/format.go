package broker

import "strconv"

// formatUnknown renders an unmapped disconnect reason code into a
// stable, sortable bucket label. Pulled into its own file so the
// payload type file stays declarations-only — no behavior in
// payload files.
func formatUnknown(code int) string {
	return "unknown_" + strconv.Itoa(code)
}
