package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_AllowWithinBurst(t *testing.T) {
	rl := NewRateLimiter(10, 5)

	// All 5 requests within burst should be allowed.
	for i := 0; i < 5; i++ {
		if !rl.Allow("client-a") {
			t.Fatalf("request %d should be allowed (within burst of 5)", i+1)
		}
	}
}

func TestRateLimiter_RejectOverBurst(t *testing.T) {
	rl := NewRateLimiter(10, 5)

	// Exhaust the burst.
	for i := 0; i < 5; i++ {
		if !rl.Allow("client-a") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// The 6th request should be rejected.
	if rl.Allow("client-a") {
		t.Fatal("request 6 should be rejected (burst exhausted)")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	// Rate of 10 tokens/sec, burst of 5.
	rl := NewRateLimiter(10, 5)

	// Exhaust the burst.
	for i := 0; i < 5; i++ {
		rl.Allow("client-a")
	}

	// Confirm we are exhausted.
	if rl.Allow("client-a") {
		t.Fatal("should be exhausted after 5 requests")
	}

	// Manually advance the last check time to simulate 1 second passing.
	// This adds 10 tokens (rate=10/s), but capped at burst=5.
	rl.mu.Lock()
	rl.buckets["client-a"].lastCheck = time.Now().Add(-1 * time.Second)
	rl.mu.Unlock()

	// After refill, requests should be allowed again.
	if !rl.Allow("client-a") {
		t.Fatal("request should be allowed after token refill")
	}
}

func TestRateLimiter_PerKeyIsolation(t *testing.T) {
	rl := NewRateLimiter(10, 3)

	// Exhaust client-a's bucket.
	for i := 0; i < 3; i++ {
		rl.Allow("client-a")
	}
	if rl.Allow("client-a") {
		t.Fatal("client-a should be exhausted")
	}

	// client-b should still have a full bucket.
	if !rl.Allow("client-b") {
		t.Fatal("client-b should be allowed (isolated from client-a)")
	}
}

func TestRateLimitMiddleware_Returns429(t *testing.T) {
	rl := NewRateLimiter(10, 2)

	// A simple inner handler that always returns 200.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := RateLimitMiddleware(rl, inner)

	// First 2 requests should succeed.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// Third request should get 429.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	// Verify JSON error body.
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body["error"] != "rate limit exceeded" {
		t.Fatalf("unexpected error: %q", body["error"])
	}
}

func TestRateLimitMiddlewareWithExemptions_BypassesHealthReadinessAndMetrics(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RateLimitMiddlewareWithExemptions(rl, []string{"/healthz", "/readyz", "/metrics"}, inner)

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			for i := 0; i < 3; i++ {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.RemoteAddr = "10.0.0.50:12345"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("request %d: expected exempt path to bypass limiter, got %d", i+1, rec.Code)
				}
			}
		})
	}
}

func TestRateLimiter_EvictsStaleEntries(t *testing.T) {
	rl := NewRateLimiterWithTTL(100, 200, 100*time.Millisecond)
	defer rl.Stop()
	for i := 0; i < 1000; i++ {
		rl.Allow(fmt.Sprintf("ip:%d.%d.%d.%d", i, i, i, i))
	}
	if rl.BucketCount() != 1000 {
		t.Fatalf("expected 1000 buckets before eviction, got %d", rl.BucketCount())
	}
	time.Sleep(250 * time.Millisecond)
	if count := rl.BucketCount(); count > 0 {
		t.Errorf("expected 0 buckets after TTL expiry, got %d", count)
	}
}

func TestRateLimiter_ActiveBucketsNotEvicted(t *testing.T) {
	rl := NewRateLimiterWithTTL(100, 200, 500*time.Millisecond)
	defer rl.Stop()
	rl.Allow("ip:active")
	time.Sleep(200 * time.Millisecond)
	rl.Allow("ip:active") // refresh lastAccess
	time.Sleep(400 * time.Millisecond)
	if rl.BucketCount() != 1 {
		t.Errorf("active bucket should survive eviction, got %d buckets", rl.BucketCount())
	}
}

func TestRateLimiter_StopPreventsLeak(t *testing.T) {
	rl := NewRateLimiterWithTTL(100, 200, 100*time.Millisecond)
	rl.Stop()
	// After Stop(), calling Allow should still work (no panic).
	if !rl.Allow("ip:after-stop") {
		t.Error("Allow should still work after Stop")
	}
}
