package core

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync/atomic"
	"time"
)

// CaptureInfo is the framework-agnostic description of an HTTP error,
// passed from middleware adapters to the client.
type CaptureInfo struct {
	Method     string
	Path       string
	StatusCode int
	Duration   time.Duration
	ClientIP   string
	UserAgent  string
	Headers    map[string]string
	PanicValue any   // non-nil when a panic was recovered
	Error      error // non-nil when an explicit error is available
}

// Client is a Vigil error monitoring client. Use NewClient for dependency
// injection or multiple instances. Use Init/the package-level functions for
// a convenient global client.
type Client struct {
	cfg      Config
	events   chan *Event
	stop     chan struct{}
	flushReq chan chan error
	done     chan struct{}
	dropped  atomic.Int64
}

// NewClient validates cfg, applies defaults, and returns a ready-to-start client.
// Call Start() before capturing any errors.
func NewClient(cfg Config) (*Client, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Notifier == nil {
		switch {
		case cfg.Slack.WebhookURL != "":
			cfg.Notifier = NewSlackNotifier(cfg.Slack)
		default:
			cfg.Notifier = NewSMTPNotifier(cfg.SMTP, cfg.Recipients)
		}
	}
	return &Client{
		cfg:      cfg,
		events:   make(chan *Event, cfg.BufferSize),
		stop:     make(chan struct{}),
		flushReq: make(chan chan error, 1),
		done:     make(chan struct{}),
	}, nil
}

// Start spawns the background event loop goroutine. It must be called once.
func (c *Client) Start() {
	go c.loop()
}

// ShouldCapture reports whether the given HTTP status code should be captured.
func (c *Client) ShouldCapture(statusCode int) bool {
	if len(c.cfg.CaptureStatusCodes) > 0 {
		return slices.Contains(c.cfg.CaptureStatusCodes, statusCode)
	}
	return statusCode >= 500
}

// ShouldIgnorePath reports whether the given path should be skipped.
func (c *Client) ShouldIgnorePath(path string) bool {
	return slices.Contains(c.cfg.IgnorePaths, path)
}

// CaptureError records an error with an optional set of tags.
// It never blocks or panics — if the buffer is full, the event is silently dropped.
func (c *Client) CaptureError(err error, tags ...map[string]string) {
	if err == nil {
		return
	}
	event := &Event{
		ID:          newID(),
		Timestamp:   time.Now(),
		Severity:    SeverityError,
		Error:       err.Error(),
		ErrorType:   errorTypeName(err),
		Stack:       captureStack(1, c.cfg.MaxStackDepth),
		ServiceName: c.cfg.ServiceName,
		Environment: c.cfg.Environment,
		Version:     c.cfg.Version,
		Hostname:    globalHostname,
		Tags:        mergeTags(tags...),
	}
	c.sendEvent(event)
}

// CaptureMessage records a free-form message at the given severity.
func (c *Client) CaptureMessage(msg string, severity Severity, tags ...map[string]string) {
	event := &Event{
		ID:          newID(),
		Timestamp:   time.Now(),
		Severity:    severity,
		Error:       msg,
		ServiceName: c.cfg.ServiceName,
		Environment: c.cfg.Environment,
		Version:     c.cfg.Version,
		Hostname:    globalHostname,
		Tags:        mergeTags(tags...),
	}
	c.sendEvent(event)
}

// CaptureHTTPError is called by middleware adapters to record an HTTP error or panic.
func (c *Client) CaptureHTTPError(info CaptureInfo) {
	event := &Event{
		ID:          newID(),
		Timestamp:   time.Now(),
		ServiceName: c.cfg.ServiceName,
		Environment: c.cfg.Environment,
		Version:     c.cfg.Version,
		Hostname:    globalHostname,
		Request: &RequestInfo{
			Method:     info.Method,
			Path:       info.Path,
			StatusCode: info.StatusCode,
			Duration:   info.Duration,
			ClientIP:   info.ClientIP,
			UserAgent:  info.UserAgent,
			Headers:    info.Headers,
		},
	}

	switch {
	case info.PanicValue != nil:
		event.Severity = SeverityFatal
		event.Error = fmt.Sprintf("panic: %v", info.PanicValue)
		event.ErrorType = "panic"
		event.Stack = captureStack(4, c.cfg.MaxStackDepth)
	case info.Error != nil:
		event.Severity = SeverityError
		event.Error = info.Error.Error()
		event.ErrorType = errorTypeName(info.Error)
		event.Stack = captureStack(4, c.cfg.MaxStackDepth)
	default:
		event.Severity = SeverityError
		event.Error = fmt.Sprintf("HTTP %d", info.StatusCode)
	}

	c.sendEvent(event)
}

// Flush forces an immediate digest send of all currently buffered events and waits
// for delivery. It does NOT stop the client.
func (c *Client) Flush(timeout time.Duration) error {
	replyCh := make(chan error, 1)
	select {
	case c.flushReq <- replyCh:
	case <-time.After(timeout):
		return fmt.Errorf("vigil: flush timed out enqueuing request")
	}
	select {
	case err := <-replyCh:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("vigil: flush timed out waiting for digest")
	}
}

// Shutdown gracefully stops the event loop: drains buffered events, sends a final
// digest, and waits up to timeout for completion.
func (c *Client) Shutdown(timeout time.Duration) error {
	close(c.stop)
	select {
	case <-c.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("vigil: shutdown timed out")
	}
}

// sendEvent applies the BeforeSend hook (if configured) then non-blocking sends to
// the events channel. Drops the event (with counter) if the channel is full.
func (c *Client) sendEvent(event *Event) {
	if c.cfg.BeforeSend != nil {
		event = c.cfg.BeforeSend(event)
		if event == nil {
			return
		}
	}
	select {
	case c.events <- event:
	default:
		c.dropped.Add(1)
	}
}

// maxConcurrentImmediate bounds the number of goroutines spawned for
// ImmediateOnFirst alerts to prevent exhaustion during error storms.
const maxConcurrentImmediate = 5

// loop is the single background goroutine that owns all mutable state.
func (c *Client) loop() {
	defer close(c.done)

	dedup := newDedupMap(c.cfg.DeduplicationTTL)
	ticker := time.NewTicker(c.cfg.DigestInterval)
	defer ticker.Stop()

	// Semaphore to bound concurrent immediate-alert goroutines.
	immediateSem := make(chan struct{}, maxConcurrentImmediate)

	periodStart := time.Now()
	totalEvents := 0

	// Per-minute rate limiter (owned by this goroutine — no mutex needed).
	var rateLimitWindowStart time.Time
	rateLimitCount := 0

	processEvent := func(event *Event) {
		now := event.Timestamp
		if rateLimitWindowStart.IsZero() || now.Sub(rateLimitWindowStart) > time.Minute {
			rateLimitWindowStart = now
			rateLimitCount = 0
		}
		rateLimitCount++
		if rateLimitCount > c.cfg.MaxEventsPerMinute {
			c.dropped.Add(1)
			return
		}

		totalEvents++
		event.Fingerprint = computeFingerprint(event)

		if c.cfg.ImmediateOnFirst && dedup.isNew(event.Fingerprint) {
			select {
			case immediateSem <- struct{}{}:
				go func(e *Event) {
					defer func() { <-immediateSem }()
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					_ = c.cfg.Notifier.SendImmediate(ctx, e)
				}(event)
			default:
				// At concurrency limit — skip immediate alert; event still goes into digest.
			}
		}
		dedup.record(event)
	}

	buildDigest := func(end time.Time) *Digest {
		groups := dedup.drainGroups()
		if len(groups) == 0 {
			return nil
		}
		sort.Slice(groups, func(i, j int) bool {
			return groups[i].Count > groups[j].Count
		})
		if len(groups) > c.cfg.MaxDigestErrors {
			groups = groups[:c.cfg.MaxDigestErrors]
		}
		digest := &Digest{
			ServiceName: c.cfg.ServiceName,
			Environment: c.cfg.Environment,
			Version:     c.cfg.Version,
			Hostname:    globalHostname,
			PeriodStart: periodStart,
			PeriodEnd:   end,
			Groups:      groups,
			TotalEvents: totalEvents,
			Dropped:     int(c.dropped.Swap(0)),
		}
		totalEvents = 0
		periodStart = end
		return digest
	}

	// sendDigestSync sends synchronously — used for flush/shutdown where the
	// caller is waiting for a reply.
	sendDigestSync := func(end time.Time) error {
		digest := buildDigest(end)
		if digest == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return c.cfg.Notifier.SendDigest(ctx, digest)
	}

	// sendDigestAsync sends in a background goroutine so the event loop stays
	// responsive. Used for periodic ticker-driven digests.
	sendDigestAsync := func(end time.Time) {
		digest := buildDigest(end)
		if digest == nil {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = c.cfg.Notifier.SendDigest(ctx, digest)
		}()
	}

	for {
		select {
		case event := <-c.events:
			processEvent(event)

		case now := <-ticker.C:
			sendDigestAsync(now)
			dedup.evict(now)

		case replyCh := <-c.flushReq:
		drainLoop:
			for {
				select {
				case event := <-c.events:
					processEvent(event)
				default:
					break drainLoop
				}
			}
			replyCh <- sendDigestSync(time.Now())

		case <-c.stop:
		shutdownDrain:
			for {
				select {
				case event := <-c.events:
					processEvent(event)
				default:
					break shutdownDrain
				}
			}
			_ = sendDigestSync(time.Now())
			return
		}
	}
}
