package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter implements a per-key token bucket rate limiter.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64 // tokens per second
	burst   int     // max bucket size
}

type tokenBucket struct {
	tokens    float64
	lastCheck time.Time
}

// NewRateLimiter creates a new per-key rate limiter. ratePerSecond is the
// sustained rate (tokens refilled per second) and burstSize is the maximum
// number of tokens a bucket can hold (i.e. the burst capacity).
func NewRateLimiter(ratePerSecond float64, burstSize int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    ratePerSecond,
		burst:   burstSize,
	}
}

// Allow checks if a request from the given key is allowed. It consumes one
// token from the key's bucket and returns true if the token was available.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		// First request for this key: start with a full bucket minus one token.
		rl.buckets[key] = &tokenBucket{
			tokens:    float64(rl.burst) - 1,
			lastCheck: now,
		}
		return true
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastCheck = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// extractRateLimitKey determines the rate-limit key from the request.
// It prefers the x-llm-d-inference-fairness-id header for per-tenant
// limiting, falling back to X-Forwarded-For or RemoteAddr for per-IP.
func extractRateLimitKey(r *http.Request) string {
	if tenantID := r.Header.Get("x-llm-d-inference-fairness-id"); tenantID != "" {
		return "tenant:" + tenantID
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Use the first (client) IP from the chain.
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return "ip:" + strings.TrimSpace(xff[:idx])
		}
		return "ip:" + strings.TrimSpace(xff)
	}
	// Strip port from RemoteAddr (host:port).
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[:idx]
	}
	return "ip:" + addr
}

// RateLimitMiddleware wraps an HTTP handler with rate limiting.
// Key extraction: uses x-llm-d-inference-fairness-id for per-tenant,
// X-Forwarded-For or RemoteAddr for per-IP rate limiting.
// Returns 429 Too Many Requests with a JSON body when the rate is exceeded.
func RateLimitMiddleware(limiter *RateLimiter, next http.Handler) http.Handler {
	return RateLimitMiddlewareWithExemptions(limiter, nil, next)
}

// RateLimitMiddlewareWithExemptions wraps an HTTP handler with rate limiting,
// bypassing exact-match exempt paths such as liveness/readiness probes.
func RateLimitMiddlewareWithExemptions(limiter *RateLimiter, exempt []string, next http.Handler) http.Handler {
	exemptSet := make(map[string]bool, len(exempt))
	for _, p := range exempt {
		exemptSet[p] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exemptSet[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		key := extractRateLimitKey(r)
		if !limiter.Allow(key) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":  "rate limit exceeded",
				"detail": "too many requests, please retry later",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
