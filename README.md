# Vigil

*"Vigil keeps watch so you don't have to."*

A zero-dependency, embeddable Go library for error monitoring. No dashboard, no SaaS account — just email alerts. Drop it into any Go HTTP server and it silently captures panics, 5xx errors, and explicit error reports, then sends digest emails.

## Features

- **Framework-agnostic** — works with Gin, Echo, `net/http`, or anything else
- **Zero config to start** — `Init()` + one middleware line = working error monitoring
- **No noise** — digest-based emails batch errors into one message per interval (default: 5 minutes)
- **Smart deduplication** — same error fires once per digest window with an occurrence count
- **Fingerprinting** — groups errors by type + call stack, not by message or line number
- **Email or Slack** — built-in SMTP and Slack webhook notifiers; plug in your own via the `Notifier` interface
- **No performance impact** — non-blocking channel send; sub-microsecond overhead on the happy path
- **No external dependencies** — core uses only the Go standard library

## Installation

```bash
go get github.com/domains18/vigil
```

For Gin middleware:

```bash
go get github.com/domains18/vigil/ginmw
```

## Quick Start

```go
package main

import (
    "log"
    "os"
    "time"

    "github.com/domains18/vigil"
    "github.com/domains18/vigil/ginmw"
    "github.com/gin-gonic/gin"
)

func main() {
    err := vigil.Init(vigil.Config{
        ServiceName: "my-api",
        Environment: "production",
        Version:     "v1.0.0",
        SMTP: vigil.SMTPConfig{
            Host:     "smtp.gmail.com",
            Port:     587,
            Username: os.Getenv("SMTP_USER"),
            Password: os.Getenv("SMTP_PASS"),
            From:     "alerts@mydomain.com",
        },
        Recipients:  []string{"oncall@mydomain.com"},
        IgnorePaths: []string{"/health", "/metrics"},
    })
    if err != nil {
        log.Fatal(err)
    }
    defer vigil.Shutdown(10 * time.Second)

    r := gin.New()
    r.Use(gin.Recovery())
    r.Use(ginmw.Middleware(vigil.DefaultClient()))

    r.GET("/api/data", func(c *gin.Context) {
        data, err := fetchData()
        if err != nil {
            vigil.CaptureError(err, map[string]string{"source": "fetchData"})
            c.JSON(500, gin.H{"error": "internal error"})
            return
        }
        c.JSON(200, data)
    })

    r.Run(":8080")
}
```

### With Slack

```go
err := vigil.Init(vigil.Config{
    ServiceName: "my-api",
    Environment: "production",
    Slack: vigil.SlackConfig{
        WebhookURL: os.Getenv("SLACK_WEBHOOK"),
        Channel:    "#alerts",
    },
    IgnorePaths: []string{"/health", "/metrics"},
})
```

## Email Digest

Vigil batches errors into a single email per digest interval:

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
VIGIL ERROR DIGEST
Service: my-api | Env: production | v1.0.0
Period: 2026-04-01 14:00 → 14:05 UTC
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

NEW — *net.OpError (23 occurrences)
   "dial tcp 10.0.1.5:5432: connection refused"
   First: 14:01:03 | Last: 14:04:58
   POST /api/conversations/find-or-create → 500
   Stack:
     repository.(*ConversationRepository).FindOrCreate
     services.(*ConversationService).FindOrCreate
     handlers.(*ConversationHandler).FindOrCreate

──────────────────────────────────────────

RECURRING — panic: runtime error (2 occurrences)
   "index out of range [3] with length 2"
   POST /api/campaigns/:id/send → 500
   Stack:
     services.(*CampaignService).buildRecipientList

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Total: 25 events | 0 dropped
Host: ip-10-0-1-42
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## Configuration

```go
vigil.Config{
    // Required
    ServiceName: "my-api",
    Environment: "production",

    // Optional
    Version: "v1.2.3",

    // SMTP delivery (use SMTP or Slack, not both)
    SMTP: vigil.SMTPConfig{
        Host:     "smtp.gmail.com",
        Port:     587,       // 587 = STARTTLS, 465 = implicit TLS
        Username: "...",
        Password: "...",
        From:     "alerts@example.com",
        UseTLS:   false,     // false = STARTTLS (port 587)
    },
    Recipients: []string{"team@example.com"},

    // Slack delivery (alternative to SMTP)
    Slack: vigil.SlackConfig{
        WebhookURL: os.Getenv("SLACK_WEBHOOK"), // https://hooks.slack.com/services/...
        Channel:    "#alerts",                  // optional — override webhook default
        Username:   "vigil",                    // optional — override webhook default
    },

    // Behavior
    DigestInterval:   5 * time.Minute, // how often to send digest emails
    ImmediateOnFirst: false,           // also send an immediate alert on first occurrence
    MaxDigestErrors:  50,              // max unique error groups per email

    // Capture rules
    IgnorePaths:        []string{"/health", "/metrics"},
    DisablePanicCapture: false,

    // Rate limiting
    MaxEventsPerMinute: 100,
    DeduplicationTTL:   time.Hour,

    // Performance
    BufferSize:    1024, // channel capacity; drops oldest events when full
    MaxStackDepth: 32,

    // Advanced
    Notifier:   myCustomNotifier, // overrides SMTP; implement the Notifier interface
    BeforeSend: func(e *vigil.Event) *vigil.Event {
        // Modify the event, or return nil to drop it entirely
        return e
    },
}
```

## Manual Error Capture

```go
// Capture an error with optional tags
if err != nil {
    vigil.CaptureError(err, map[string]string{
        "user_id":    userID,
        "channel_id": channelID,
    })
}

// Force a specific fingerprint (groups all matching tags together)
vigil.CaptureError(err, map[string]string{
    "vigil.fingerprint": "payment-timeout",
})

// Capture a plain message
vigil.CaptureMessage("cache miss rate exceeded threshold", vigil.SeverityWarning)
```

## Custom Notifier

Implement `vigil.Notifier` to send alerts via PagerDuty, Discord, a custom webhook, or anything else:

```go
type PagerDutyNotifier struct{ routingKey string }

func (p *PagerDutyNotifier) SendDigest(ctx context.Context, d *vigil.Digest) error {
    // send a PagerDuty event for the digest
}

func (p *PagerDutyNotifier) SendImmediate(ctx context.Context, e *vigil.Event) error {
    // trigger a PagerDuty incident for critical errors
}

err := vigil.Init(vigil.Config{
    ServiceName: "my-api",
    Environment: "production",
    Notifier:    &PagerDutyNotifier{routingKey: os.Getenv("PD_ROUTING_KEY")},
})
```

## Middleware Adapters

### Gin

```go
import "github.com/domains18/vigil/ginmw"

r.Use(gin.Recovery())                        // outermost
r.Use(ginmw.Middleware(vigil.DefaultClient())) // innermost — observes panics before Recovery
```

> Vigil must be registered **after** `gin.Recovery()`. Go's defers are LIFO, so the innermost middleware's recovery fires first, letting Vigil capture panics as `SeverityFatal` before `gin.Recovery` swallows them.

### net/http and Echo

Coming in Phase 2 (`nethttpmw` and `echomw` packages).

## Dependency Injection

Use `NewClient` instead of `Init` to manage the lifecycle yourself:

```go
client, err := vigil.NewClient(vigil.Config{...})
if err != nil {
    return err
}
client.Start()
defer client.Shutdown(10 * time.Second)

// Pass to middleware
r.Use(ginmw.Middleware(client))

// Capture manually
client.CaptureError(err)
```

## License

MIT — see [LICENSE](LICENSE).
