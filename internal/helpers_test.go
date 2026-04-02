package core

import (
	"context"
	"sync"
	"time"
)

// noopNotifier discards all notifications. Used in config/validation tests.
type noopNotifier struct{}

func (n *noopNotifier) SendDigest(_ context.Context, _ *Digest) error   { return nil }
func (n *noopNotifier) SendImmediate(_ context.Context, _ *Event) error { return nil }

// recordingNotifier captures sent digests and immediate alerts for assertions.
type recordingNotifier struct {
	mu         sync.Mutex
	digests    []*Digest
	immediates []*Event
}

func (r *recordingNotifier) SendDigest(_ context.Context, d *Digest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.digests = append(r.digests, d)
	return nil
}

func (r *recordingNotifier) SendImmediate(_ context.Context, e *Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.immediates = append(r.immediates, e)
	return nil
}

func (r *recordingNotifier) digestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.digests)
}

func (r *recordingNotifier) lastDigest() *Digest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.digests) == 0 {
		return nil
	}
	return r.digests[len(r.digests)-1]
}

func (r *recordingNotifier) immediateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.immediates)
}

// newTestClient creates a started client with a short digest interval and a
// recordingNotifier for fast, deterministic tests.
func newTestClient() (*Client, *recordingNotifier) {
	rec := &recordingNotifier{}
	cfg := Config{
		ServiceName:        "test-svc",
		Environment:        "test",
		DigestInterval:     50 * time.Millisecond,
		DeduplicationTTL:   time.Hour,
		BufferSize:         256,
		MaxStackDepth:      8,
		MaxDigestErrors:    50,
		MaxEventsPerMinute: 1000,
		Notifier:           rec,
	}
	cfg.applyDefaults()
	c := &Client{
		cfg:      cfg,
		events:   make(chan *Event, cfg.BufferSize),
		stop:     make(chan struct{}),
		flushReq: make(chan chan error, 1),
		done:     make(chan struct{}),
	}
	return c, rec
}
