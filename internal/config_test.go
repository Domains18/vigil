package core

import (
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := Config{
		ServiceName: "svc",
		Environment: "test",
		Notifier:    &noopNotifier{},
	}
	cfg.applyDefaults()

	if cfg.DigestInterval != 5*time.Minute {
		t.Errorf("DigestInterval: got %v, want 5m", cfg.DigestInterval)
	}
	if cfg.MaxDigestErrors != 50 {
		t.Errorf("MaxDigestErrors: got %d, want 50", cfg.MaxDigestErrors)
	}
	if cfg.MaxEventsPerMinute != 100 {
		t.Errorf("MaxEventsPerMinute: got %d, want 100", cfg.MaxEventsPerMinute)
	}
	if cfg.DeduplicationTTL != time.Hour {
		t.Errorf("DeduplicationTTL: got %v, want 1h", cfg.DeduplicationTTL)
	}
	if cfg.BufferSize != 1024 {
		t.Errorf("BufferSize: got %d, want 1024", cfg.BufferSize)
	}
	if cfg.MaxStackDepth != 32 {
		t.Errorf("MaxStackDepth: got %d, want 32", cfg.MaxStackDepth)
	}
}

func TestConfigValidation(t *testing.T) {
	base := Config{
		ServiceName: "svc",
		Environment: "test",
		Notifier:    &noopNotifier{},
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid with custom notifier",
			mutate:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "missing service name",
			mutate:  func(c *Config) { c.ServiceName = "" },
			wantErr: true,
		},
		{
			name:    "missing environment",
			mutate:  func(c *Config) { c.Environment = "" },
			wantErr: true,
		},
		{
			name: "no notifier, no recipients",
			mutate: func(c *Config) {
				c.Notifier = nil
				c.SMTP = SMTPConfig{Host: "smtp.example.com", Port: 587, From: "a@b.com"}
			},
			wantErr: true,
		},
		{
			name: "no notifier, no smtp host",
			mutate: func(c *Config) {
				c.Notifier = nil
				c.Recipients = []string{"a@b.com"}
			},
			wantErr: true,
		},
		{
			name: "no notifier, valid smtp",
			mutate: func(c *Config) {
				c.Notifier = nil
				c.Recipients = []string{"a@b.com"}
				c.SMTP = SMTPConfig{Host: "smtp.example.com", Port: 587, From: "a@b.com"}
			},
			wantErr: false,
		},
		{
			name: "no notifier, valid slack webhook",
			mutate: func(c *Config) {
				c.Notifier = nil
				c.Slack = SlackConfig{WebhookURL: "https://hooks.slack.com/services/T00/B00/xxx"}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			cfg.applyDefaults()
			err := cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
