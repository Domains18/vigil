package core

// Severity represents the level of an error event.
type Severity int

const (
	SeverityInfo    Severity = iota // informational; not necessarily an error
	SeverityWarning                 // something unexpected that may need attention
	SeverityError                   // a recoverable error
	SeverityFatal                   // an unrecoverable error or panic
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	case SeverityFatal:
		return "fatal"
	default:
		return "unknown"
	}
}
