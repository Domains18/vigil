package vigil

import (
	"context"
	"time"
)

// Notifier is the interface for sending error notifications.
// Implement this to send alerts via Slack, Discord, PagerDuty, etc.
type Notifier interface {
	SendDigest(ctx context.Context, digest *Digest) error
	SendImmediate(ctx context.Context, event *Event) error
}

// Digest is a batch of grouped errors for a time window.
type Digest struct {
	ServiceName string
	Environment string
	Version     string
	Hostname    string

	PeriodStart time.Time
	PeriodEnd   time.Time

	Groups      []*ErrorGroup
	TotalEvents int
	Dropped     int
}

// ErrorGroup is a set of deduplicated errors sharing the same fingerprint.
type ErrorGroup struct {
	Fingerprint string
	Count       int
	IsNew       bool // true if this fingerprint has never appeared in a prior window
	FirstSeen   time.Time
	LastSeen    time.Time
	Sample      *Event // one representative event for context
}
