package services

import "strconv"

// MapDisconnectReason translates a Mosquitto disconnect reason code
// (MOSQ_DISCONNECT_*) into a human-readable text label. Unmapped codes
// return "unknown_<code>" so dashboards have a stable bucket for new broker
// firmware values without code changes.
func MapDisconnectReason(code int) string {
	switch code {
	case 0:
		return "clean_disconnect"
	case 4:
		return "keepalive_timeout"
	case 142:
		return "session_taken_over"
	case 152:
		return "admin_action"
	default:
		return formatUnknown(code)
	}
}

// formatUnknown renders an unmapped disconnect reason code into a stable,
// sortable bucket label.
func formatUnknown(code int) string {
	return "unknown_" + strconv.Itoa(code)
}
