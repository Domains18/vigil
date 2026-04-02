package core

import (
	"crypto/rand"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"time"
)

// Event represents a captured error or message.
type Event struct {
	ID          string
	Fingerprint string
	Timestamp   time.Time
	Severity    Severity

	// Error info
	Error     string // error.Error() message
	ErrorType string // Go type name, e.g. "*url.Error"
	Stack     []Frame

	// HTTP context (nil for manual captures outside HTTP)
	Request *RequestInfo

	// Service metadata
	ServiceName string
	Environment string
	Version     string
	Hostname    string

	// User-supplied context
	Tags  map[string]string
	Extra map[string]any
}

// Frame is a single stack frame.
type Frame struct {
	Function string // e.g. "services.(*MetaClient).SendMessage"
	File     string // e.g. "/app/internal/services/metaClient.go"
	Line     int
}

// RequestInfo contains HTTP request context captured by middleware.
type RequestInfo struct {
	Method     string
	URL        string
	Path       string
	StatusCode int
	Duration   time.Duration
	ClientIP   string
	UserAgent  string
	Headers    map[string]string // filtered: no Authorization, Cookie, etc.
}

var globalHostname string

func init() {
	globalHostname, _ = os.Hostname()
}

// newID returns a random hex ID.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// captureStack captures up to depth frames, skipping skip+2 frames above the caller.
func captureStack(skip, depth int) []Frame {
	pcs := make([]uintptr, depth)
	n := runtime.Callers(skip+2, pcs)
	pcs = pcs[:n]

	iter := runtime.CallersFrames(pcs)
	result := make([]Frame, 0, n)
	for {
		f, more := iter.Next()
		result = append(result, Frame{
			Function: f.Function,
			File:     f.File,
			Line:     f.Line,
		})
		if !more {
			break
		}
	}
	return result
}

// errorTypeName returns the Go type name of an error, e.g. "*net.OpError".
func errorTypeName(err error) string {
	if err == nil {
		return ""
	}
	return reflect.TypeOf(err).String()
}

// mergeTags merges variadic tag maps into one. Later maps win on key conflicts.
func mergeTags(tags ...map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	result := make(map[string]string)
	for _, t := range tags {
		for k, v := range t {
			result[k] = v
		}
	}
	return result
}
