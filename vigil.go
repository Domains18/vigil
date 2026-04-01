// Package vigil is a zero-dependency, embeddable Go library for error monitoring.
// Drop it into any Go HTTP server and it silently captures panics, 5xx errors,
// and explicit error reports, then sends digest emails.
//
// Quick start:
//
//	err := vigil.Init(vigil.Config{
//	    ServiceName: "my-api",
//	    Environment: "production",
//	    SMTP: vigil.SMTPConfig{
//	        Host: "smtp.gmail.com", Port: 587,
//	        Username: "...", Password: "...", From: "alerts@example.com",
//	    },
//	    Recipients: []string{"me@example.com"},
//	})
//	defer vigil.Shutdown(10 * time.Second)
package vigil

import (
	"sync"
	"time"
)

var (
	globalMu     sync.RWMutex
	globalClient *Client
)

// Init creates and starts the global client. It must be called once at startup.
// Use Shutdown to flush and stop the client on program exit.
func Init(cfg Config) error {
	c, err := NewClient(cfg)
	if err != nil {
		return err
	}
	c.Start()

	globalMu.Lock()
	globalClient = c
	globalMu.Unlock()
	return nil
}

// DefaultClient returns the global client created by Init, or nil if Init has
// not been called. Useful for passing to middleware adapters.
func DefaultClient() *Client {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalClient
}

// CaptureError records an error using the global client.
// It is safe to call before Init — the call is a no-op.
func CaptureError(err error, tags ...map[string]string) {
	if c := DefaultClient(); c != nil {
		c.CaptureError(err, tags...)
	}
}

// CaptureMessage records a free-form message using the global client.
func CaptureMessage(msg string, severity Severity, tags ...map[string]string) {
	if c := DefaultClient(); c != nil {
		c.CaptureMessage(msg, severity, tags...)
	}
}

// Flush forces an immediate digest send without shutting down the global client.
func Flush(timeout time.Duration) error {
	if c := DefaultClient(); c != nil {
		return c.Flush(timeout)
	}
	return nil
}

// Shutdown flushes pending events and stops the global client.
func Shutdown(timeout time.Duration) error {
	globalMu.Lock()
	c := globalClient
	globalClient = nil
	globalMu.Unlock()

	if c != nil {
		return c.Shutdown(timeout)
	}
	return nil
}
