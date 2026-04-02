package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlackNotifierSendDigest(t *testing.T) {
	var received slackPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(SlackConfig{
		WebhookURL: srv.URL,
		Channel:    "#alerts",
		Username:   "vigil-bot",
	})

	digest := &Digest{
		ServiceName: "test-svc",
		Environment: "production",
		PeriodStart: time.Now().Add(-5 * time.Minute),
		PeriodEnd:   time.Now(),
		Groups: []*ErrorGroup{
			{
				Fingerprint: "abc123",
				Count:       3,
				IsNew:       true,
				FirstSeen:   time.Now().Add(-4 * time.Minute),
				LastSeen:    time.Now(),
				Sample: &Event{
					Error:     "connection refused",
					ErrorType: "*net.OpError",
				},
			},
		},
		TotalEvents: 3,
	}

	if err := n.SendDigest(context.Background(), digest); err != nil {
		t.Fatalf("SendDigest: %v", err)
	}

	if received.Channel != "#alerts" {
		t.Errorf("channel: got %q, want #alerts", received.Channel)
	}
	if received.Username != "vigil-bot" {
		t.Errorf("username: got %q, want vigil-bot", received.Username)
	}
	if received.Text == "" {
		t.Error("expected non-empty text")
	}
}

func TestSlackNotifierSendImmediate(t *testing.T) {
	var received slackPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(SlackConfig{WebhookURL: srv.URL})

	event := &Event{
		ID:          "test-123",
		Timestamp:   time.Now(),
		Severity:    SeverityError,
		Error:       "panic: nil pointer",
		ErrorType:   "panic",
		ServiceName: "test-svc",
		Environment: "staging",
	}

	if err := n.SendImmediate(context.Background(), event); err != nil {
		t.Fatalf("SendImmediate: %v", err)
	}
	if received.Text == "" {
		t.Error("expected non-empty text")
	}
}

func TestSlackNotifierHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewSlackNotifier(SlackConfig{WebhookURL: srv.URL})
	err := n.SendImmediate(context.Background(), &Event{
		Timestamp:   time.Now(),
		ServiceName: "test",
		Environment: "test",
		Error:       "test error",
	})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}
