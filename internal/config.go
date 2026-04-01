package core

import (
	"errors"
	"time"
)

// Config holds all configuration for a Vigil client.
type Config struct {
	// Service identification (required)
	ServiceName string
	Environment string
	Version     string // optional, e.g. "v1.2.3"

	// Email delivery
	SMTP       SMTPConfig
	Recipients []string // required when Notifier is nil

	// Behavior
	DigestInterval   time.Duration // default: 5 minutes
	ImmediateOnFirst bool          // default: false
	MaxDigestErrors  int           // default: 50 (max unique error groups per email)

	// Capture rules
	CaptureStatusCodes  []int    // default: nil (capture all >= 500)
	DisablePanicCapture bool     // default: false (panics ARE captured)
	IgnorePaths         []string // paths to skip, e.g. ["/health", "/metrics"]

	// Rate limiting
	MaxEventsPerMinute int           // default: 100
	DeduplicationTTL   time.Duration // default: 1 hour

	// Performance
	BufferSize    int // default: 1024 (events channel capacity)
	MaxStackDepth int // default: 32 (frames to capture)

	// Extensibility
	Notifier   Notifier            // optional — overrides SMTP notifier
	BeforeSend func(*Event) *Event // optional — modify or filter events (return nil to drop)
}

// SMTPConfig holds SMTP connection settings.
type SMTPConfig struct {
	Host     string
	Port     int    // typically 587 (STARTTLS) or 465 (implicit TLS)
	Username string
	Password string
	From     string // e.g. "alerts@yourdomain.com"
	UseTLS   bool   // true = implicit TLS (port 465); false = STARTTLS (port 587)
}

// applyDefaults fills in zero-value fields with their defaults.
func (c *Config) applyDefaults() {
	if c.DigestInterval == 0 {
		c.DigestInterval = 5 * time.Minute
	}
	if c.MaxDigestErrors == 0 {
		c.MaxDigestErrors = 50
	}
	if c.MaxEventsPerMinute == 0 {
		c.MaxEventsPerMinute = 100
	}
	if c.DeduplicationTTL == 0 {
		c.DeduplicationTTL = time.Hour
	}
	if c.BufferSize == 0 {
		c.BufferSize = 1024
	}
	if c.MaxStackDepth == 0 {
		c.MaxStackDepth = 32
	}
}

// validate returns an error if the config is invalid.
func (c *Config) validate() error {
	if c.ServiceName == "" {
		return errors.New("vigil: ServiceName is required")
	}
	if c.Environment == "" {
		return errors.New("vigil: Environment is required")
	}
	if c.Notifier == nil {
		if len(c.Recipients) == 0 {
			return errors.New("vigil: Recipients required when no custom Notifier is set")
		}
		if c.SMTP.Host == "" {
			return errors.New("vigil: SMTP.Host required when no custom Notifier is set")
		}
		if c.SMTP.Port == 0 {
			return errors.New("vigil: SMTP.Port required when no custom Notifier is set")
		}
		if c.SMTP.From == "" {
			return errors.New("vigil: SMTP.From required when no custom Notifier is set")
		}
	}
	return nil
}
