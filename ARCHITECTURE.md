# Vigil — Architecture

*"Vigil keeps watch so you don't have to."*

A zero-dependency, embeddable Go library for error monitoring. No UI, no dashboard — just email alerts. Drop it into any Go HTTP server (Gin, Echo, net/http) and it silently captures panics, 5xx errors, and explicit error reports, then sends digest emails.

---

## Design Goals

1. **Framework-agnostic** — works with any Go HTTP server, not tied to a specific project
2. **Zero config to start** — `Init()` + one middleware line = working error monitoring
3. **No noise** — digest-based emails, deduplication, fingerprinting to avoid inbox flooding
4. **No performance impact** — sub-microsecond overhead on the happy path
5. **No external dependencies** — core uses only stdlib; framework adapters are separate subpackages

---

## Project Structure

```
vigil/
├── vigil.go              # Public API: Init(), CaptureError(), CaptureMessage(), Shutdown()
├── config.go             # Configuration types, defaults, validation
├── client.go             # Core client struct, event loop goroutine, lifecycle
├── event.go              # Error event type, stack trace capture via runtime.Callers()
├── fingerprint.go        # Error grouping/fingerprinting algorithm
├── dedup.go              # Deduplication map with TTL and occurrence counting
├── severity.go           # Severity levels: Info, Warning, Error, Fatal
├── notifier.go           # Notifier interface + digest types
├── mail/
│   ├── smtp.go           # SMTP notifier implementation
│   └── template.go       # Digest email templates (HTML + plain text)
├── ginmw/
│   └── gin.go            # Gin middleware adapter
├── echomw/
│   └── echo.go           # Echo middleware adapter
├── nethttpmw/
│   └── nethttp.go        # net/http middleware adapter
├── examples/
│   ├── gin/main.go       # Example: Gin integration
│   ├── nethttp/main.go   # Example: net/http integration
│   └── echo/main.go      # Example: Echo integration
├── go.mod
├── ARCHITECTURE.md        # This file
└── README.md              # Usage docs (created after Phase 1)
```

### Subpackage Strategy

The core `vigil` package has **zero dependencies** beyond Go's stdlib. Framework middleware lives in separate subpackages (`ginmw`, `echomw`, `nethttpmw`) so importing Vigil into a net/http app never pulls in Gin or Echo.

```
github.com/your-org/vigil              → no external deps
github.com/your-org/vigil/ginmw        → depends on gin-gonic/gin
github.com/your-org/vigil/echomw       → depends on labstack/echo/v4
github.com/your-org/vigil/nethttpmw    → no external deps
```

---

## Core Architecture

### The Event Loop (client.go)

The central design is a single goroutine that owns all mutable state, communicating via buffered channels. This eliminates lock contention on the hot path entirely.

```
  HTTP Middleware / CaptureError() / CaptureMessage()
                    │
                    ▼
          ┌──────────────────┐
          │  Buffered Channel │  capacity: configurable (default 1024)
          │   events chan      │
          └────────┬─────────┘
                   │
                   ▼
  ┌────────────────────────────────────────┐
  │         Event Loop Goroutine           │
  │                                        │
  │  On event received:                    │
  │    1. Compute fingerprint              │
  │    2. Update dedup map (count++)       │
  │    3. If first occurrence & immediate  │
  │       mode → send email now            │
  │    4. Add to current digest buffer     │
  │                                        │
  │  On ticker (every DigestInterval):     │
  │    5. Build Digest from grouped events │
  │    6. Send digest email via Notifier   │
  │    7. Clear digest buffer              │
  │    8. Evict expired dedup entries      │
  │                                        │
  │  On stop signal:                       │
  │    9. Drain remaining events           │
  │   10. Flush final digest               │
  │   11. Signal completion                │
  └────────────────────────────────────────┘
```

**Why channels, not mutexes?** The capture path (steps 1-4) is on the hot path of every HTTP request. A channel send is non-blocking when the buffer isn't full. The event loop goroutine is the sole reader and writer of the dedup map and digest buffer — no synchronization needed. If the buffer is full, the event is dropped and a counter is incremented (reported in the next digest).

### Lifecycle

```go
// Startup
client := vigil.NewClient(cfg)  // validates config, creates channels
client.Start()                   // spawns event loop goroutine

// Shutdown (called from main's graceful shutdown)
client.Shutdown(10 * time.Second)  // signals stop, flushes buffer, waits with timeout
```

`Init()` is a convenience that creates a global client. `NewClient()` is for dependency injection or multiple instances.

---

## Error Capture Flow

### From HTTP Middleware

```
Request arrives
    │
    ▼
┌─────────────────────────────┐
│  Vigil Middleware            │
│                              │
│  1. Record start time        │
│  2. defer panic recovery     │
│  3. Call next handler        │
│  4. Check response status    │
│     - if >= 500 (default):   │
│       build Event, send to   │
│       channel                │
│     - if panic recovered:    │
│       build Event with       │
│       Fatal severity, send   │
│       to channel, re-panic   │
└─────────────────────────────┘
```

### From Application Code (Manual Capture)

```go
if err != nil {
    vigil.CaptureError(err, map[string]string{
        "channel_id": channelID,
        "user_id":    userID,
    })
    // execution continues normally — Vigil never blocks or panics
}
```

---

## Fingerprinting Algorithm (fingerprint.go)

The fingerprint determines which errors are "the same" for grouping and deduplication. This is the most critical algorithm in the system.

### For Go Errors (with stack traces)

1. Take the error type name: `*net.OpError`, `*json.SyntaxError`, etc.
2. Take the top N stack frames (default: 3), using only `package.Function` — strip file paths and line numbers
3. Concatenate: `errorType|func1|func2|func3`
4. SHA-256 → truncate to 16 hex characters

**Why strip line numbers?** The same bug on line 142 vs line 145 (after a minor refactor) is the same bug. Grouping by function name is more stable.

### For HTTP Errors (no Go error, just status code)

1. Take HTTP method + normalized path + status code
2. Path normalization rules:
   - UUIDs → `:id` (`/api/users/550e8400-e29b-41d4-a716-446655440000` → `/api/users/:id`)
   - Numeric segments → `:id` (`/api/orders/12345` → `/api/orders/:id`)
3. Result: `POST /api/users/:id/messages 500`
4. SHA-256 → truncate to 16 hex characters

### Manual Override

```go
vigil.CaptureError(err, map[string]string{
    "vigil.fingerprint": "payment-timeout", // forces grouping key
})
```

---

## Deduplication (dedup.go)

An in-memory map owned exclusively by the event loop goroutine (no mutex needed):

```go
type dedupEntry struct {
    fingerprint string
    count       int        // total occurrences in current digest window
    firstSeen   time.Time  // within this window
    lastSeen    time.Time
    sampleEvent *Event     // one full event for context in the email
    everSeen    bool       // has this fingerprint appeared in a previous window?
}
```

**Lifecycle:**
- On each event: find-or-create entry, increment count, update lastSeen
- On each digest tick: drain entries into Digest, clear counts, keep fingerprints in memory for `DeduplicationTTL` (default: 1 hour) to distinguish "new" vs "recurring"
- Entries older than TTL are evicted on each tick

**Why TTL?** Without it, the map grows unbounded. With it, if an error disappears for an hour and returns, it's flagged as "new" again — which is useful.

---

## Notification Strategy (notifier.go, mail/)

### Digest-Based (Default)

One email per digest interval (default: 5 minutes) containing all grouped errors:

```
Subject: [Vigil] myapp/production — 3 errors (47 occurrences) in 5m

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
VIGIL ERROR DIGEST
Service: myapp | Env: production | v1.2.3
Period: 2026-04-01 14:00 → 14:05 UTC
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

NEW — *net.OpError (23 occurrences)
   "dial tcp 10.0.1.5:5432: connection refused"
   First: 14:01:03 | Last: 14:04:58
   POST /api/conversations/find-or-create → 500
   Stack:
     repository.(*ConversationRepository).FindOrCreate
     services.(*ConversationService).FindOrCreatePlatform
     handlers.(*ConversationHandler).FindOrCreate

──────────────────────────────────────────

RECURRING — *json.SyntaxError (22 occurrences)
   "invalid character '<' looking for beginning of value"
   First: 14:00:12 | Last: 14:04:44
   POST /api/webhooks/meta → 500
   Stack:
     services.(*WebhookService).processMessage
     services.(*WebhookService).ReceiveWebhook

──────────────────────────────────────────

RECURRING — panic: runtime error (2 occurrences)
   "index out of range [3] with length 2"
   POST /api/campaigns/:id/send → 500
   Stack:
     services.(*CampaignService).buildRecipientList

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Total: 47 errors | 0 dropped
Host: ip-10-0-1-42
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### Immediate Mode (Opt-in)

When `ImmediateOnFirst: true`, the very first occurrence of a never-before-seen error also triggers an immediate email. Subsequent occurrences of that error are batched into digests only.

### Notifier Interface

```go
type Notifier interface {
    SendDigest(ctx context.Context, digest *Digest) error
    SendImmediate(ctx context.Context, event *Event) error
}
```

The SMTP mailer implements this. Users can provide their own for Slack, Discord, PagerDuty, etc.

---

## Configuration (config.go)

```go
type Config struct {
    // Service identification
    ServiceName string        // required — e.g. "myapp", "payment-api"
    Environment string        // required — e.g. "production", "staging"
    Version     string        // optional — e.g. "v1.2.3"

    // Email
    SMTP       SMTPConfig
    Recipients []string       // required — email addresses to notify

    // Behavior
    DigestInterval   time.Duration // default: 5 minutes
    ImmediateOnFirst bool          // default: false
    MaxDigestErrors  int           // default: 50 (max unique errors per email)

    // Capture
    CaptureStatusCodes []int    // default: 500+ (all 5xx)
    CapturePanics      bool     // default: true
    IgnorePaths        []string // default: none — e.g. ["/health", "/metrics"]

    // Rate limiting
    MaxEventsPerMinute int           // default: 100
    DeduplicationTTL   time.Duration // default: 1 hour

    // Performance
    BufferSize    int // default: 1024 (channel capacity)
    MaxStackDepth int // default: 32 (frames to capture)

    // Extensibility
    Notifier   Notifier                  // optional — overrides SMTP
    BeforeSend func(*Event) *Event       // optional — modify/filter events (return nil to drop)
}

type SMTPConfig struct {
    Host     string
    Port     int    // typically 587 (STARTTLS) or 465 (TLS)
    Username string
    Password string
    From     string // e.g. "alerts@yourdomain.com"
    UseTLS   bool   // default: true
}
```

---

## Event Type (event.go)

```go
type Event struct {
    ID          string
    Fingerprint string
    Timestamp   time.Time
    Severity    Severity     // Info, Warning, Error, Fatal

    // Error
    Error     string         // error message
    ErrorType string         // Go type name, e.g. "*url.Error"
    Stack     []Frame        // captured stack trace

    // HTTP context (nil for manual captures outside HTTP)
    Request *RequestInfo

    // Environment
    ServiceName string
    Environment string
    Version     string
    Hostname    string

    // User-supplied context
    Tags  map[string]string
    Extra map[string]any
}

type Frame struct {
    Function string // e.g. "services.(*MetaClient).SendMessage"
    File     string // e.g. "/app/internal/services/metaClient.go"
    Line     int
}

type RequestInfo struct {
    Method     string
    URL        string
    Path       string
    StatusCode int
    Duration   time.Duration
    ClientIP   string
    UserAgent  string
    Headers    map[string]string // filtered: no Authorization, Cookie, etc.
}
```

---

## Middleware Architecture

All framework adapters delegate to a single framework-agnostic capture method on the client:

```go
func (c *Client) captureHTTPError(info CaptureInfo)

type CaptureInfo struct {
    Method      string
    Path        string
    StatusCode  int
    Duration    time.Duration
    ClientIP    string
    UserAgent   string
    Headers     map[string]string
    PanicValue  any    // nil if no panic
    Error       error  // nil if no explicit error
}
```

### Gin Adapter (ginmw/gin.go)

```go
func Middleware(client *vigil.Client) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        defer func() {
            if r := recover(); r != nil {
                client.captureHTTPError(/* ... panic info ... */)
                panic(r) // re-panic for gin.Recovery()
            }
        }()

        c.Next()

        if client.ShouldCapture(c.Writer.Status()) {
            client.captureHTTPError(/* ... request info ... */)
        }
    }
}
```

### net/http Adapter (nethttpmw/nethttp.go)

Wraps `http.ResponseWriter` to capture status code (since stdlib doesn't expose it):

```go
func Middleware(client *vigil.Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            rw := &responseWriter{ResponseWriter: w, status: 200}
            // ... same pattern: defer recovery, call next, check status
        })
    }
}
```

### Echo Adapter (echomw/echo.go)

Uses Echo's `echo.MiddlewareFunc` and `c.Response().Status`.

### Middleware Ordering

Vigil's middleware should be registered **before** the framework's recovery middleware so panics are observed before being swallowed:

```go
r.Use(ginmw.Middleware(client))  // Vigil first
r.Use(gin.Recovery())             // framework recovery second
```

---

## Public API (vigil.go)

Intentionally small:

```go
// Global client (convenience)
func Init(cfg Config) error
func CaptureError(err error, tags ...map[string]string)
func CaptureMessage(msg string, severity Severity, tags ...map[string]string)
func Flush(timeout time.Duration) error   // flush pending events without shutdown
func Shutdown(timeout time.Duration) error

// Instance client (for DI or multiple instances)
func NewClient(cfg Config) (*Client, error)
func (c *Client) CaptureError(err error, tags ...map[string]string)
func (c *Client) CaptureMessage(msg string, severity Severity, tags ...map[string]string)
func (c *Client) ShouldCapture(statusCode int) bool
func (c *Client) Flush(timeout time.Duration) error
func (c *Client) Shutdown(timeout time.Duration) error
```

---

## Example Integration (any Go server)

```go
package main

import (
    "github.com/your-org/vigil"
    "github.com/your-org/vigil/ginmw"
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
            UseTLS:   true,
        },
        Recipients:  []string{"me@email.com"},
        IgnorePaths: []string{"/health", "/metrics"},
    })
    if err != nil {
        log.Fatal(err)
    }
    defer vigil.Shutdown(10 * time.Second)

    r := gin.New()
    r.Use(ginmw.Middleware(vigil.DefaultClient()))
    r.Use(gin.Recovery())

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

---

## Performance Budget

| Scenario | Target |
|---|---|
| Happy path (no error) | < 1 microsecond added latency, zero heap allocations |
| Error captured | ~5-10 microseconds (stack trace), 1 allocation, 1 non-blocking channel send |
| Channel buffer full | Drop event + increment counter (reported in next digest) |
| Memory overhead | < 1 MB under normal load |

---

## Implementation Phases

### Phase 1: Core Engine (MVP)

Everything needed for a working product.

**Files:**
- `vigil.go` — global API
- `config.go` — config struct, defaults, validation
- `client.go` — event loop goroutine, Start/Shutdown lifecycle
- `event.go` — Event struct, stack trace capture via `runtime.Callers()`
- `fingerprint.go` — fingerprinting with stack normalization + path normalization
- `severity.go` — severity enum
- `dedup.go` — dedup map with TTL and counting
- `notifier.go` — Notifier interface, Digest/ErrorGroup types
- `mail/smtp.go` — SMTP notifier
- `mail/template.go` — email templates
- `ginmw/gin.go` — Gin middleware

**Tests:**
- Config validation
- Fingerprint stability and normalization
- Dedup counting and TTL eviction
- Event loop: digest timing, shutdown flush
- Gin middleware: panic capture, status code capture

**Estimated:** ~1500 lines of Go

### Phase 2: Framework Adapters + Manual Capture Polish

- `nethttpmw/nethttp.go` — net/http middleware with response writer wrapper
- `echomw/echo.go` — Echo middleware
- `BeforeSend` hook for filtering/modification
- Examples for all three frameworks

### Phase 3: Smart Deduplication + Filtering

- Configurable fingerprint depth
- Error sampling (1-in-N after threshold for very noisy errors)
- Path pattern heuristics (detect `:id` even for non-UUID segments)
- Source context: optionally include a few lines of code around the error site

### Phase 4: Reliability

- SQLite-backed buffer (survive process crashes)
- SMTP retry with exponential backoff
- Dead-letter file logging (if email fails after retries, write to disk)
- Prometheus-compatible metrics: errors captured, emails sent, buffer depth, dropped count

### Phase 5: Additional Notifiers

- Slack webhook notifier
- Discord webhook notifier
- Generic HTTP webhook notifier (POST JSON to any URL)
- PagerDuty for Fatal-severity
- Multi-notifier: send to email AND Slack simultaneously

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Emails land in spam | Digest batching reduces volume; use a From domain with SPF/DKIM configured |
| SMTP server unavailable | Retry with backoff; dead-letter to log file; Phase 4 adds SQLite persistence |
| Fingerprint groups too broadly | Configurable frame depth; manual fingerprint override via tags; `BeforeSend` hook |
| Fingerprint groups too narrowly | Strip line numbers from frames; normalize paths; UUID/numeric detection |
| Dedup map grows unbounded | TTL eviction; max entries cap; cleanup on every digest tick |
| Channel buffer overflow under burst | Drop + count; report drops in digest; configurable buffer size |
| Framework middleware import bloat | Separate subpackages — core never imports Gin/Echo |
