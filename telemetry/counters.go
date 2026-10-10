// Package telemetry is the local MCP server's usage counting: counters are kept in a local file and, only if the user turned uploading on, sent in batches about once a minute while the server is in use, with a random installation id and the name of the MCP client. The counters that may be uploaded are the fixed set below, listed in the README ("What leaves the machine, and when") and enforced by the API, so what leaves a machine is never more than a handful of counts.
package telemetry

import "strings"

// Counter names. A counter is a count of events in one period; none carries a value from a drawing.
const (
	// CounterChecks is checks run.
	CounterChecks = "checks"
	// CounterChecksRecorded is checks the user chose to record on the service.
	CounterChecksRecorded = "checks_recorded"
	// CounterFeedback is feedback reports filed.
	CounterFeedback = "feedback"
	// CounterErrors is checks the engine refused (a malformed file, a bad request).
	CounterErrors = "errors"
	// Per-value counters are a prefix and one of the engine's enum values, such as verdict:manufacturable or format:dxf.
	PrefixVerdict = "verdict:"
	PrefixFormat  = "format:"
	PrefixProcess = "process:"
)

// Allowed reports whether a counter name is one the service accepts.
func Allowed(name string) bool {
	switch name {
	case CounterChecks, CounterChecksRecorded, CounterFeedback, CounterErrors:
		return true
	}
	for _, prefix := range []string{PrefixVerdict, PrefixFormat, PrefixProcess} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest != "" && len(rest) <= 40 && isSnake(rest)
		}
	}
	return false
}

// MaxCounters bounds a report, well above the number of distinct names a period can produce.
const MaxCounters = 64

func isSnake(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
