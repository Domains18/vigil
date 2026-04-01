package ginmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/domains18/vigil"
	"github.com/domains18/vigil/ginmw"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// captureNotifier records events for test assertions.
type captureNotifier struct {
	mu         sync.Mutex
	digests    []*vigil.Digest
	immediates []*vigil.Event
}

func (n *captureNotifier) SendDigest(_ context.Context, d *vigil.Digest) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.digests = append(n.digests, d)
	return nil
}

func (n *captureNotifier) SendImmediate(_ context.Context, e *vigil.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.immediates = append(n.immediates, e)
	return nil
}

func newTestClient(t *testing.T) (*vigil.Client, *captureNotifier) {
	t.Helper()
	rec := &captureNotifier{}
	c, err := vigil.NewClient(vigil.Config{
		ServiceName:        "test",
		Environment:        "test",
		DigestInterval:     10 * time.Second, // long — we use Flush instead
		BufferSize:         64,
		MaxStackDepth:      8,
		MaxDigestErrors:    50,
		MaxEventsPerMinute: 1000,
		Notifier:           rec,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.Start()
	t.Cleanup(func() { c.Shutdown(3 * time.Second) })
	return c, rec
}

func TestMiddleware5xx(t *testing.T) {
	client, rec := newTestClient(t)

	r := gin.New()
	r.Use(ginmw.Middleware(client))
	r.GET("/boom", func(c *gin.Context) {
		c.Status(http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if err := client.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.digests) == 0 {
		t.Fatal("expected a digest after 500 response")
	}
	d := rec.digests[len(rec.digests)-1]
	if len(d.Groups) == 0 {
		t.Fatal("digest has no error groups")
	}
	g := d.Groups[0]
	if g.Sample.Request == nil {
		t.Fatal("expected request info in sample event")
	}
	if g.Sample.Request.StatusCode != 500 {
		t.Errorf("status code: got %d, want 500", g.Sample.Request.StatusCode)
	}
	if g.Sample.Request.Method != http.MethodGet {
		t.Errorf("method: got %q, want GET", g.Sample.Request.Method)
	}
}

func TestMiddleware2xxNotCaptured(t *testing.T) {
	client, rec := newTestClient(t)

	r := gin.New()
	r.Use(ginmw.Middleware(client))
	r.GET("/ok", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err := client.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, d := range rec.digests {
		if len(d.Groups) > 0 {
			t.Errorf("did not expect any captured errors for 2xx response, got %+v", d.Groups)
		}
	}
}

func TestMiddlewarePanicCapture(t *testing.T) {
	client, rec := newTestClient(t)

	r := gin.New()
	r.Use(ginmw.Middleware(client))
	r.Use(gin.Recovery()) // must come after Vigil so panic is observed first
	r.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err := client.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.digests) == 0 {
		t.Fatal("expected digest after panic")
	}
	d := rec.digests[len(rec.digests)-1]
	if len(d.Groups) == 0 {
		t.Fatal("expected error group for panic")
	}
	g := d.Groups[0]
	if g.Sample.Severity != vigil.SeverityFatal {
		t.Errorf("severity: got %v, want Fatal", g.Sample.Severity)
	}
}

func TestMiddlewareIgnorePath(t *testing.T) {
	client, rec := newTestClient(t)
	client2, err := vigil.NewClient(vigil.Config{
		ServiceName:        "test",
		Environment:        "test",
		DigestInterval:     10 * time.Second,
		BufferSize:         64,
		MaxStackDepth:      8,
		MaxDigestErrors:    50,
		MaxEventsPerMinute: 1000,
		IgnorePaths:        []string{"/health"},
		Notifier:           rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	client2.Start()
	t.Cleanup(func() { client2.Shutdown(3 * time.Second) })
	_ = client

	r := gin.New()
	r.Use(ginmw.Middleware(client2))
	r.GET("/health", func(c *gin.Context) { c.Status(500) })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err := client2.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, d := range rec.digests {
		if len(d.Groups) > 0 {
			t.Error("ignored path should not produce error groups")
		}
	}
}

func TestMiddlewareRouteTemplate(t *testing.T) {
	client, rec := newTestClient(t)

	r := gin.New()
	r.Use(ginmw.Middleware(client))
	r.GET("/api/users/:id", func(c *gin.Context) {
		c.Status(http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err := client.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.digests) == 0 {
		t.Fatal("expected digest")
	}
	path := rec.digests[len(rec.digests)-1].Groups[0].Sample.Request.Path
	if path != "/api/users/:id" {
		t.Errorf("path: got %q, want /api/users/:id (route template)", path)
	}
}
