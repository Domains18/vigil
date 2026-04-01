package vigil

import (
	"testing"
	"time"
)

func TestFingerprintStability(t *testing.T) {
	// Same error type + same stack = same fingerprint, regardless of line numbers.
	frames := []Frame{
		{Function: "github.com/example/app/services.(*MetaClient).SendMessage", File: "services/meta.go", Line: 142},
		{Function: "github.com/example/app/services.(*WebhookService).ReceiveWebhook", File: "services/webhook.go", Line: 88},
		{Function: "github.com/example/app/handlers.(*WebhookHandler).Handle", File: "handlers/webhook.go", Line: 55},
	}
	framesShifted := []Frame{
		{Function: "github.com/example/app/services.(*MetaClient).SendMessage", File: "services/meta.go", Line: 145}, // line changed
		{Function: "github.com/example/app/services.(*WebhookService).ReceiveWebhook", File: "services/webhook.go", Line: 91},
		{Function: "github.com/example/app/handlers.(*WebhookHandler).Handle", File: "handlers/webhook.go", Line: 58},
	}

	e1 := &Event{ErrorType: "*net.OpError", Stack: frames, Timestamp: time.Now()}
	e2 := &Event{ErrorType: "*net.OpError", Stack: framesShifted, Timestamp: time.Now()}

	fp1 := computeFingerprint(e1)
	fp2 := computeFingerprint(e2)

	if fp1 != fp2 {
		t.Errorf("fingerprints differ after line number change: %q vs %q", fp1, fp2)
	}
}

func TestFingerprintDifferentErrors(t *testing.T) {
	frames := []Frame{
		{Function: "github.com/example/app/services.DoSomething", File: "services/x.go", Line: 10},
	}
	e1 := &Event{ErrorType: "*net.OpError", Stack: frames, Timestamp: time.Now()}
	e2 := &Event{ErrorType: "*json.SyntaxError", Stack: frames, Timestamp: time.Now()}

	if computeFingerprint(e1) == computeFingerprint(e2) {
		t.Error("different error types with same stack should have different fingerprints")
	}
}

func TestFingerprintManualOverride(t *testing.T) {
	e := &Event{
		ErrorType: "*net.OpError",
		Stack:     []Frame{{Function: "foo.Bar"}},
		Tags:      map[string]string{"vigil.fingerprint": "my-custom-key"},
		Timestamp: time.Now(),
	}
	if fp := computeFingerprint(e); fp != "my-custom-key" {
		t.Errorf("manual fingerprint override ignored: got %q", fp)
	}
}

func TestFingerprintHTTPRequest(t *testing.T) {
	// Events with no stack should use HTTP request info.
	e := &Event{
		Request: &RequestInfo{
			Method:     "POST",
			Path:       "/api/users/12345/messages",
			StatusCode: 500,
		},
		Timestamp: time.Now(),
	}
	fp := computeFingerprint(e)
	if fp == "" {
		t.Error("fingerprint should not be empty")
	}

	// A different numeric ID should yield the same fingerprint (path normalized).
	e2 := &Event{
		Request: &RequestInfo{
			Method:     "POST",
			Path:       "/api/users/99999/messages",
			StatusCode: 500,
		},
		Timestamp: time.Now(),
	}
	if computeFingerprint(e) != computeFingerprint(e2) {
		t.Error("numeric path segments should normalize to the same fingerprint")
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/api/users/12345", "/api/users/:id"},
		{"/api/users/550e8400-e29b-41d4-a716-446655440000", "/api/users/:id"},
		{"/api/orders/42/items/7", "/api/orders/:id/items/:id"},
		{"/health", "/health"},
		{"/api/users/me", "/api/users/me"},
	}
	for _, tt := range tests {
		got := NormalizePath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFingerprintLength(t *testing.T) {
	e := &Event{Error: "some random error", Timestamp: time.Now()}
	fp := computeFingerprint(e)
	if len(fp) != 16 {
		t.Errorf("fingerprint length: got %d, want 16", len(fp))
	}
}
