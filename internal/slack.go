package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SlackConfig holds settings for the Slack webhook notifier.
type SlackConfig struct {
	WebhookURL string // Slack incoming webhook URL
	Channel    string // optional — override webhook's default channel
	Username   string // optional — override webhook's default username
}

// SlackNotifier implements Notifier by posting to a Slack incoming webhook.
// Zero external dependencies — uses net/http from the standard library.
type SlackNotifier struct {
	cfg    SlackConfig
	client *http.Client
}

// NewSlackNotifier creates a Slack notifier from the given config.
func NewSlackNotifier(cfg SlackConfig) *SlackNotifier {
	return &SlackNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// slackPayload is the JSON body posted to the Slack webhook.
type slackPayload struct {
	Channel  string         `json:"channel,omitempty"`
	Username string         `json:"username,omitempty"`
	Text     string         `json:"text"`
	Blocks   []slackBlock   `json:"blocks,omitempty"`
}

type slackBlock struct {
	Type string          `json:"type"`
	Text *slackBlockText `json:"text,omitempty"`
}

type slackBlockText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (n *SlackNotifier) SendDigest(ctx context.Context, digest *Digest) error {
	text := renderSlackDigest(digest)
	return n.post(ctx, text)
}

func (n *SlackNotifier) SendImmediate(ctx context.Context, event *Event) error {
	text := renderSlackImmediate(event)
	return n.post(ctx, text)
}

func (n *SlackNotifier) post(ctx context.Context, text string) error {
	payload := slackPayload{
		Channel:  n.cfg.Channel,
		Username: n.cfg.Username,
		Text:     text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("vigil slack: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vigil slack: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("vigil slack: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("vigil slack: webhook returned %d", resp.StatusCode)
	}
	return nil
}

// renderSlackDigest formats a Digest as a Slack mrkdwn message.
func renderSlackDigest(digest *Digest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "*VIGIL ERROR DIGEST*\n")
	fmt.Fprintf(&b, "*Service:* %s | *Env:* %s", digest.ServiceName, digest.Environment)
	if digest.Version != "" {
		fmt.Fprintf(&b, " | %s", digest.Version)
	}
	fmt.Fprintf(&b, "\n*Period:* %s → %s UTC\n",
		digest.PeriodStart.UTC().Format("2006-01-02 15:04"),
		digest.PeriodEnd.UTC().Format("2006-01-02 15:04"))

	for _, g := range digest.Groups {
		status := "RECURRING"
		if g.IsNew {
			status = "🆕 NEW"
		}

		e := g.Sample
		errorType := e.ErrorType
		if errorType == "" {
			errorType = "error"
		}

		fmt.Fprintf(&b, "\n%s — `%s` (%d occurrences)\n", status, errorType, g.Count)
		if e.Error != "" {
			fmt.Fprintf(&b, "> %s\n", e.Error)
		}
		fmt.Fprintf(&b, "_First: %s | Last: %s_\n",
			g.FirstSeen.UTC().Format("15:04:05"),
			g.LastSeen.UTC().Format("15:04:05"))

		if e.Request != nil {
			fmt.Fprintf(&b, "`%s %s → %d`\n",
				e.Request.Method, NormalizePath(e.Request.Path), e.Request.StatusCode)
		}

		if len(e.Stack) > 0 {
			fmt.Fprintf(&b, "```\n")
			for j, frame := range e.Stack {
				if j >= fingerprintFrameDepth {
					break
				}
				fmt.Fprintf(&b, "%s\n", normalizeFunction(frame.Function))
			}
			fmt.Fprintf(&b, "```\n")
		}
	}

	fmt.Fprintf(&b, "\n*Total:* %d events | %d dropped", digest.TotalEvents, digest.Dropped)
	if digest.Hostname != "" {
		fmt.Fprintf(&b, " | *Host:* %s", digest.Hostname)
	}

	return b.String()
}

// renderSlackImmediate formats a single Event as a Slack mrkdwn message.
func renderSlackImmediate(event *Event) string {
	var b strings.Builder

	fmt.Fprintf(&b, "🚨 *VIGIL — IMMEDIATE ALERT*\n")
	fmt.Fprintf(&b, "*Service:* %s | *Env:* %s\n", event.ServiceName, event.Environment)
	fmt.Fprintf(&b, "*Time:* %s UTC\n", event.Timestamp.UTC().Format("2006-01-02 15:04:05"))

	if event.ErrorType != "" {
		fmt.Fprintf(&b, "*Type:* `%s`\n", event.ErrorType)
	}
	fmt.Fprintf(&b, "*Error:* %s\n", event.Error)

	if event.Request != nil {
		fmt.Fprintf(&b, "\n*HTTP:* `%s %s → %d` (%s)\n",
			event.Request.Method, event.Request.Path,
			event.Request.StatusCode,
			event.Request.Duration.Round(time.Millisecond))
	}

	if len(event.Stack) > 0 {
		fmt.Fprintf(&b, "\n*Stack:*\n```\n")
		for i, frame := range event.Stack {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "%s\n  %s:%d\n", frame.Function, frame.File, frame.Line)
		}
		fmt.Fprintf(&b, "```\n")
	}

	return b.String()
}
