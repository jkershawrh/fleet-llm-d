package server

import (
	"log/slog"
	"net/http"
	"time"
)

// SECURITY: Request/response body content MUST NEVER be logged.
//
// The fleet controller processes inference requests that may contain
// sensitive prompt content, PII, or proprietary data. All request logging
// is restricted to operational metadata only:
//
//   - HTTP method
//   - URL path (never query parameters which may carry tokens)
//   - Response status code
//   - Request latency
//   - Client IP (for rate-limiting correlation)
//
// Do NOT add body logging, header value logging (which may contain
// Authorization tokens), or query parameter logging to any middleware
// in this package. If debug-level request inspection is needed during
// development, use a local proxy (e.g. mitmproxy) rather than adding
// log statements that could reach production.

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// RequestLoggingMiddleware logs each request with safe operational metadata
// only: method, path, status, and latency. It MUST NOT log request or
// response bodies, query parameters, or header values.
func RequestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(recorder, r)

		// SECURITY: only log method, path, status, and latency.
		// Never log r.Body, r.URL.RawQuery, r.Header, or any
		// field that may contain inference content or credentials.
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode,
			"latency_ms", time.Since(start).Milliseconds(),
		)
	})
}
