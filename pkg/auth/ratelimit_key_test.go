package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// TestRateLimitIgnoresCallerSuppliedIdentity is the regression guard for the
// bypass: the limiter used to key on the x-llm-d-inference-fairness-id header,
// so a client that varied it per request got a fresh full bucket every time
// and was never limited.
func TestRateLimitIgnoresCallerSuppliedIdentity(t *testing.T) {
	limiter := NewRateLimiter(0, 3) // burst 3, no refill
	defer limiter.Stop()
	handler := RateLimitMiddlewareWithExemptions(limiter, nil, okHandler())

	var limited bool
	for i := 0; i < 12; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
		req.RemoteAddr = "203.0.113.9:44100"
		// A different forged identity on every request.
		req.Header.Set("x-llm-d-inference-fairness-id", string(rune('a'+i)))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("varying a caller-supplied header bypassed the rate limit")
	}
	if got := limiter.BucketCount(); got != 1 {
		t.Fatalf("bucket count = %d, want 1 (one client address)", got)
	}
}

// TestRateLimitIgnoresUntrustedForwardedFor asserts X-Forwarded-For is only
// honoured when the operator has explicitly opted in.
func TestRateLimitIgnoresUntrustedForwardedFor(t *testing.T) {
	limiter := NewRateLimiter(0, 2)
	defer limiter.Stop()
	handler := RateLimitMiddlewareWithExemptions(limiter, nil, okHandler())

	var limited bool
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
		req.RemoteAddr = "203.0.113.9:44100"
		req.Header.Set("X-Forwarded-For", "10.0.0."+string(rune('0'+i)))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("spoofed X-Forwarded-For bypassed the rate limit")
	}
}

// TestRateLimitHonoursForwardedForWhenTrusted covers the proxied deployment.
func TestRateLimitHonoursForwardedForWhenTrusted(t *testing.T) {
	limiter := NewRateLimiter(0, 2)
	defer limiter.Stop()
	limiter.TrustProxyHeaders(true)
	handler := RateLimitMiddlewareWithExemptions(limiter, nil, okHandler())

	for _, client := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
		req.RemoteAddr = "10.0.0.1:1234" // the proxy, shared by all clients
		req.Header.Set("X-Forwarded-For", client+", 10.0.0.1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("client %s got %d on its first request", client, rec.Code)
		}
	}
	if got := limiter.BucketCount(); got != 3 {
		t.Fatalf("bucket count = %d, want 3 (one per forwarded client)", got)
	}
}

// TestSubjectRateLimitSeparatesPrincipals asserts the post-auth tier gives
// each verified subject its own budget regardless of source address.
func TestSubjectRateLimitSeparatesPrincipals(t *testing.T) {
	limiter := NewRateLimiter(0, 2)
	defer limiter.Stop()
	handler := SubjectRateLimitMiddleware(limiter, nil, okHandler())

	request := func(subject, addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
		req.RemoteAddr = addr
		req = req.WithContext(WithClaims(req.Context(), &Claims{
			Subject: subject, Role: RoleOperator, ExpiresAt: time.Now().Add(time.Hour),
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Exhaust one subject's budget from a rotating set of addresses.
	request("tenant-a", "203.0.113.1:1000")
	request("tenant-a", "203.0.113.2:1000")
	if code := request("tenant-a", "203.0.113.3:1000"); code != http.StatusTooManyRequests {
		t.Fatalf("tenant-a third request = %d, want 429; changing source address evaded the limit", code)
	}
	// A different subject is unaffected.
	if code := request("tenant-b", "203.0.113.1:1000"); code != http.StatusOK {
		t.Fatalf("tenant-b first request = %d, want 200; budgets are not isolated", code)
	}
}

// TestRateLimitCapsBucketCardinality asserts the limiter refuses to grow its
// key map without bound under a distributed flood.
func TestRateLimitCapsBucketCardinality(t *testing.T) {
	limiter := NewRateLimiterWithTTL(0, 5, time.Hour)
	defer limiter.Stop()
	limiter.maxBuckets = 8

	for i := 0; i < 200; i++ {
		limiter.Allow("ip:198.51.100." + string(rune(i)))
	}
	if got := limiter.BucketCount(); got > 8 {
		t.Fatalf("bucket count = %d, want <= 8; the map grew past its cap", got)
	}
}
