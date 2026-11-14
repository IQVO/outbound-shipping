// Package requestlog is the access-log middleware of the HTTP servers
// (cmd/api's REST router and cmd/mcp's router). It is its own package, not a
// file of internal/adapters/inbound/http, because the MCP read-only fitness
// test forbids cmd/mcp importing any path containing inbound/http: the MCP
// binary shares this logger without pulling in the REST adapter.
package requestlog

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// sanitizeForLog strips CR/LF from an attacker-controlled value (here, the
// request path) before it is written to a log line. Without this a crafted
// path with an encoded newline could forge a fake log entry once the record
// reaches a viewer that does not preserve slog's JSON string escaping
// (CWE-117 log injection).
func sanitizeForLog(s string) string {
	return strings.NewReplacer("\n", "", "\r", "").Replace(s)
}

// RequestLogger returns chi middleware that logs each request at Info level
// (Error for 5xx responses) with method, path, status, duration, response
// size and the chi request id. It writes through the *Context logger
// variants: the request context carries the active span, and the telemetry
// slog handler reads trace_id/span_id off it.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			attrs := []any{
				"method", r.Method,
				"path", sanitizeForLog(r.URL.Path),
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", ww.BytesWritten(),
				"request_id", middleware.GetReqID(r.Context()),
			}
			if ww.Status() >= http.StatusInternalServerError {
				logger.ErrorContext(r.Context(), "http request", attrs...)
				return
			}
			logger.InfoContext(r.Context(), "http request", attrs...)
		})
	}
}
