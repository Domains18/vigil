package core

import (
	"errors"
	"testing"
	"time"
)

func TestClientCaptureError(t *testing.T) {
	c, rec := newTestClient()
	c.Start()
	defer c.Shutdown(5 * time.Second)

	c.CaptureError(errors.New("something broke"))
	if err := c.Flush(2 * time.Second); err != nil {
		t.Fatalf("flush: %v", err)
	}

	d := rec.lastDigest()
	if d == nil {
		t.Fatal("expected a digest after flush")
	}
	if len(d.Groups) != 1 {
		t.Fatalf("expected 1 error group, got %d", len(d.Groups))
	}
	if d.Groups[0].Count != 1 {
		t.Errorf("count: got %d, want 1", d.Groups[0].Count)
	}
	if d.Groups[0].Sample.Error != "something broke" {
		t.Errorf("error message: got %q", d.Groups[0].Sample.Error)
	}
}

func TestClientDeduplication(t *testing.T) {
	c, rec := newTestClient()
	c.Start()
	defer c.Shutdown(5 * time.Second)

	err := errors.New("db connection refused")
	for i := 0; i < 10; i++ {
		c.CaptureError(err)
	}
	if flushErr := c.Flush(2 * time.Second); flushErr != nil {
		t.Fatal(flushErr)
	}

	d := rec.lastDigest()
	if d == nil {
		t.Fatal("expected digest")
	}
	if len(d.Groups) != 1 {
		t.Fatalf("expected 1 error group (deduplicated), got %d", len(d.Groups))
	}
	if d.Groups[0].Count != 10 {
		t.Errorf("count: got %d, want 10", d.Groups[0].Count)
	}
}

func TestClientDigestTick(t *testing.T) {
	c, rec := newTestClient()
	c.Start()
	defer c.Shutdown(5 * time.Second)

	c.CaptureError(errors.New("tick test"))

	time.Sleep(200 * time.Millisecond)

	if rec.digestCount() == 0 {
		t.Error("expected at least one digest to fire from the ticker")
	}
}

func TestClientShutdownFlush(t *testing.T) {
	c, rec := newTestClient()
	c.Start()

	c.CaptureError(errors.New("shutdown test"))
	if err := c.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if rec.digestCount() == 0 {
		t.Error("expected a digest to be sent on shutdown")
	}
}

func TestClientBeforeSendFilter(t *testing.T) {
	c, rec := newTestClient()
	c.cfg.BeforeSend = func(e *Event) *Event { return nil }
	c.Start()
	defer c.Shutdown(5 * time.Second)

	c.CaptureError(errors.New("should be dropped"))
	if err := c.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	d := rec.lastDigest()
	if d != nil {
		t.Errorf("expected no digest when BeforeSend drops all events, got %+v", d)
	}
}

func TestClientShouldCapture(t *testing.T) {
	c, _ := newTestClient()

	tests := []struct {
		code int
		want bool
	}{
		{200, false},
		{301, false},
		{400, false},
		{404, false},
		{500, true},
		{502, true},
		{503, true},
	}
	for _, tt := range tests {
		if got := c.ShouldCapture(tt.code); got != tt.want {
			t.Errorf("ShouldCapture(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestClientShouldIgnorePath(t *testing.T) {
	c, _ := newTestClient()
	c.cfg.IgnorePaths = []string{"/health", "/metrics"}

	if !c.ShouldIgnorePath("/health") {
		t.Error("expected /health to be ignored")
	}
	if !c.ShouldIgnorePath("/metrics") {
		t.Error("expected /metrics to be ignored")
	}
	if c.ShouldIgnorePath("/api/users") {
		t.Error("expected /api/users NOT to be ignored")
	}
}

func TestClientImmediateOnFirst(t *testing.T) {
	c, rec := newTestClient()
	c.cfg.ImmediateOnFirst = true
	c.Start()
	defer c.Shutdown(5 * time.Second)

	c.CaptureError(errors.New("brand new error"))
	c.CaptureError(errors.New("brand new error"))

	time.Sleep(100 * time.Millisecond)

	if rec.immediateCount() != 1 {
		t.Errorf("immediate count: got %d, want 1", rec.immediateCount())
	}
}

func TestClientDropsWhenBufferFull(t *testing.T) {
	c, _ := newTestClient()
	c.cfg.BufferSize = 2
	c.events = make(chan *Event, 2)

	for i := 0; i < 10; i++ {
		c.sendEvent(&Event{Timestamp: time.Now(), Error: "overflow"})
	}

	if c.dropped.Load() == 0 {
		t.Error("expected some events to be dropped when buffer is full")
	}
}
