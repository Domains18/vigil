package core

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

const fingerprintFrameDepth = 3

var (
	reUUID    = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	reNumeric = regexp.MustCompile(`/\d+`)
)

// computeFingerprint derives a stable 16-hex-char fingerprint for an event.
// If the event's Tags contain "vigil.fingerprint", that value is used directly.
func computeFingerprint(event *Event) string {
	if fp, ok := event.Tags["vigil.fingerprint"]; ok && fp != "" {
		return fp
	}

	var raw string
	switch {
	case len(event.Stack) > 0:
		raw = fingerprintFromStack(event.ErrorType, event.Stack)
	case event.Request != nil:
		raw = fingerprintFromRequest(event.Request)
	default:
		raw = event.Error
	}

	return hashFingerprint(raw)
}

// fingerprintFromStack builds a fingerprint from error type + top N stack frames.
// Line numbers are intentionally excluded so minor refactors don't break grouping.
func fingerprintFromStack(errorType string, frames []Frame) string {
	parts := make([]string, 0, fingerprintFrameDepth+1)
	parts = append(parts, errorType)
	for i, f := range frames {
		if i >= fingerprintFrameDepth {
			break
		}
		parts = append(parts, normalizeFunction(f.Function))
	}
	return strings.Join(parts, "|")
}

// fingerprintFromRequest builds a fingerprint from method + normalized path + status code.
func fingerprintFromRequest(req *RequestInfo) string {
	return fmt.Sprintf("%s %s %d", req.Method, NormalizePath(req.Path), req.StatusCode)
}

// normalizeFunction strips the module path prefix, returning "pkg.(*Type).Method".
func normalizeFunction(fn string) string {
	idx := strings.LastIndex(fn, "/")
	if idx >= 0 {
		return fn[idx+1:]
	}
	return fn
}

// NormalizePath replaces UUID and numeric path segments with ":id".
// Exported so middleware adapters and templates can use it for display.
func NormalizePath(path string) string {
	path = reUUID.ReplaceAllString(path, ":id")
	path = reNumeric.ReplaceAllString(path, "/:id")
	return path
}

// hashFingerprint returns a 16 hex-char truncation of a SHA-256 hash.
func hashFingerprint(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}
