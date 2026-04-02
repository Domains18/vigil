package core

// Severity represents the level of an error event.
type Severity int

const (
	SeverityInfo    Severity = iota
	SeverityWarning Severity = iota
	SeverityError   Severity = iota
	SeverityFatal   Severity = iota
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
