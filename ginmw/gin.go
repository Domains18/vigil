// Package ginmw provides a Vigil middleware adapter for the Gin web framework.
// Import this subpackage only in applications that use Gin — the core vigil
// package has zero external dependencies.
package ginmw

import (
	"net/http"
	"strings"
	"time"

	"github.com/domains18/vigil"
	"github.com/gin-gonic/gin"
)

// sensitiveHeaders are redacted from captured request info.
var sensitiveHeaders = map[string]bool{
	"authorization": true,
	"cookie":        true,
	"set-cookie":    true,
	"x-api-key":     true,
	"x-auth-token":  true,
}

// Middleware returns a Gin handler that captures panics and 5xx responses.
// Register it before gin.Recovery() so panics are observed before being swallowed.
//
//	r.Use(ginmw.Middleware(client))
//	r.Use(gin.Recovery())
func Middleware(client *vigil.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if client.ShouldIgnorePath(path) {
			c.Next()
			return
		}

		start := time.Now()

		defer func() {
			if r := recover(); r != nil {
				client.CaptureHTTPError(vigil.CaptureInfo{
					Method:     c.Request.Method,
					Path:       routePath(c),
					StatusCode: http.StatusInternalServerError,
					Duration:   time.Since(start),
					ClientIP:   c.ClientIP(),
					UserAgent:  c.Request.UserAgent(),
					Headers:    filteredHeaders(c.Request.Header),
					PanicValue: r,
				})
				panic(r) // re-panic so gin.Recovery() can handle it
			}
		}()

		c.Next()

		status := c.Writer.Status()
		if !client.ShouldCapture(status) {
			return
		}

		info := vigil.CaptureInfo{
			Method:     c.Request.Method,
			Path:       routePath(c),
			StatusCode: status,
			Duration:   time.Since(start),
			ClientIP:   c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			Headers:    filteredHeaders(c.Request.Header),
		}
		// Attach the first framework-level error if present.
		if len(c.Errors) > 0 {
			info.Error = c.Errors.Last().Err
		}
		client.CaptureHTTPError(info)
	}
}

// routePath returns the matched route template (e.g. "/api/users/:id") when
// available, falling back to the raw request path.
func routePath(c *gin.Context) string {
	if fp := c.FullPath(); fp != "" {
		return fp
	}
	return c.Request.URL.Path
}

// filteredHeaders copies request headers, omitting sensitive ones.
func filteredHeaders(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for k, v := range h {
		if !sensitiveHeaders[strings.ToLower(k)] && len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}
