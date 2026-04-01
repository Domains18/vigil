package vigil

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// renderDigest formats a Digest as a plain-text email body.
func renderDigest(digest *Digest) string {
	var b bytes.Buffer

	heavy := strings.Repeat("━", 42)
	light := strings.Repeat("─", 42)

	fmt.Fprintf(&b, "%s\n", heavy)
	fmt.Fprintf(&b, "VIGIL ERROR DIGEST\n")
	fmt.Fprintf(&b, "Service: %s | Env: %s", digest.ServiceName, digest.Environment)
	if digest.Version != "" {
		fmt.Fprintf(&b, " | %s", digest.Version)
	}
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "Period: %s → %s UTC\n",
		digest.PeriodStart.UTC().Format("2006-01-02 15:04"),
		digest.PeriodEnd.UTC().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "%s\n", heavy)

	for i, g := range digest.Groups {
		fmt.Fprintf(&b, "\n")
		status := "RECURRING"
		if g.IsNew {
			status = "NEW"
		}

		e := g.Sample
		errorType := e.ErrorType
		if errorType == "" {
			errorType = "error"
		}

		fmt.Fprintf(&b, "%s — %s (%d occurrences)\n", status, errorType, g.Count)
		if e.Error != "" {
			fmt.Fprintf(&b, "   %q\n", e.Error)
		}
		fmt.Fprintf(&b, "   First: %s | Last: %s\n",
			g.FirstSeen.UTC().Format("15:04:05"),
			g.LastSeen.UTC().Format("15:04:05"))

		if e.Request != nil {
			fmt.Fprintf(&b, "   %s %s → %d\n",
				e.Request.Method, NormalizePath(e.Request.Path), e.Request.StatusCode)
		}

		if len(e.Stack) > 0 {
			fmt.Fprintf(&b, "   Stack:\n")
			for j, frame := range e.Stack {
				if j >= fingerprintFrameDepth {
					break
				}
				fmt.Fprintf(&b, "     %s\n", normalizeFunction(frame.Function))
			}
		}

		if i < len(digest.Groups)-1 {
			fmt.Fprintf(&b, "\n%s\n", light)
		}
	}

	fmt.Fprintf(&b, "\n%s\n", heavy)
	fmt.Fprintf(&b, "Total: %d events | %d dropped\n", digest.TotalEvents, digest.Dropped)
	if digest.Hostname != "" {
		fmt.Fprintf(&b, "Host: %s\n", digest.Hostname)
	}
	fmt.Fprintf(&b, "%s\n", heavy)

	return b.String()
}

// renderImmediate formats a single Event as a plain-text email body.
func renderImmediate(event *Event) string {
	var b bytes.Buffer

	heavy := strings.Repeat("━", 42)

	fmt.Fprintf(&b, "%s\n", heavy)
	fmt.Fprintf(&b, "VIGIL — IMMEDIATE ALERT\n")
	fmt.Fprintf(&b, "Service: %s | Env: %s\n", event.ServiceName, event.Environment)
	fmt.Fprintf(&b, "Time: %s UTC\n", event.Timestamp.UTC().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "%s\n\n", heavy)

	if event.ErrorType != "" {
		fmt.Fprintf(&b, "Type:  %s\n", event.ErrorType)
	}
	fmt.Fprintf(&b, "Error: %s\n", event.Error)

	if event.Request != nil {
		fmt.Fprintf(&b, "\nHTTP: %s %s → %d (%s)\n",
			event.Request.Method, event.Request.Path,
			event.Request.StatusCode,
			event.Request.Duration.Round(time.Millisecond))
	}

	if len(event.Stack) > 0 {
		fmt.Fprintf(&b, "\nStack:\n")
		for i, frame := range event.Stack {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  %s\n    %s:%d\n", frame.Function, frame.File, frame.Line)
		}
	}

	if len(event.Tags) > 0 {
		fmt.Fprintf(&b, "\nTags:\n")
		for k, v := range event.Tags {
			if k == "vigil.fingerprint" {
				continue
			}
			fmt.Fprintf(&b, "  %s: %s\n", k, v)
		}
	}

	fmt.Fprintf(&b, "\n%s\n", heavy)
	return b.String()
}

// digestSubject builds the email subject for a digest.
func digestSubject(digest *Digest) string {
	totalOccurrences := 0
	for _, g := range digest.Groups {
		totalOccurrences += g.Count
	}
	dur := digest.PeriodEnd.Sub(digest.PeriodStart).Round(time.Minute)
	return fmt.Sprintf("[Vigil] %s/%s — %d errors (%d occurrences) in %s",
		digest.ServiceName, digest.Environment,
		len(digest.Groups), totalOccurrences, formatDuration(dur))
}

// immediateSubject builds the email subject for an immediate alert.
func immediateSubject(event *Event) string {
	msg := event.Error
	if len(msg) > 80 {
		msg = msg[:80] + "…"
	}
	return fmt.Sprintf("[Vigil] %s/%s — %s: %s",
		event.ServiceName, event.Environment, event.Severity, msg)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
