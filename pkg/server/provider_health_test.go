package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProviderHealthCacheFailsClosedAndCaches(t *testing.T) {
	requests := 0
	status := http.StatusServiceUnavailable
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(status)
	}))
	defer backend.Close()

	cache, err := NewProviderHealthCache(map[string]string{"cpu-provider-b": backend.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	cache.ttl = time.Minute
	if healthy, configured := cache.Healthy(context.Background(), "cpu-provider-b"); !configured || healthy {
		t.Fatalf("failed probe = healthy %v, configured %v", healthy, configured)
	}
	status = http.StatusOK
	if healthy, _ := cache.Healthy(context.Background(), "cpu-provider-b"); healthy {
		t.Fatal("cached failed probe unexpectedly became healthy")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 cached probe", requests)
	}
	for i := 0; i < providerHealthyThreshold; i++ {
		cache.mu.Lock()
		entry := cache.entries["cpu-provider-b"]
		entry.checkedAt = time.Now().Add(-2 * time.Minute)
		cache.entries["cpu-provider-b"] = entry
		cache.mu.Unlock()
		cache.Healthy(context.Background(), "cpu-provider-b")
	}
	if healthy, _ := cache.Healthy(context.Background(), "cpu-provider-b"); !healthy {
		t.Fatal("provider did not recover after successful probe")
	}
	status = http.StatusServiceUnavailable
	for i := 0; i < providerUnhealthyThreshold; i++ {
		cache.mu.Lock()
		entry := cache.entries["cpu-provider-b"]
		entry.checkedAt = time.Now().Add(-2 * time.Minute)
		cache.entries["cpu-provider-b"] = entry
		cache.mu.Unlock()
		cache.Healthy(context.Background(), "cpu-provider-b")
	}
	if healthy, _ := cache.Healthy(context.Background(), "cpu-provider-b"); healthy {
		t.Fatal("provider remained healthy after consecutive failed probes")
	}
}

func TestProviderHealthCacheLeavesUnconfiguredProviderToRepositoryHealth(t *testing.T) {
	cache, err := NewProviderHealthCache(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if healthy, configured := cache.Healthy(context.Background(), "cpu-provider-a"); configured || healthy {
		t.Fatalf("unconfigured provider = healthy %v, configured %v", healthy, configured)
	}
}

func TestProviderHealthCacheRecordsHTTPFailureBeforeRecovery(t *testing.T) {
	status := http.StatusServiceUnavailable
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer backend.Close()

	cache, err := NewProviderHealthCache(map[string]string{"provider-a": backend.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	cache.ttl = 0
	if healthy := cache.probe(context.Background(), "provider-a", backend.URL); healthy {
		t.Fatal("failed first probe reported healthy")
	}
	entry := cache.entries["provider-a"]
	if entry.consecutiveFailures != 1 || entry.consecutiveSuccesses != 0 {
		t.Fatalf("failure counters = %+v", entry)
	}

	status = http.StatusOK
	for range providerHealthyThreshold {
		cache.probe(context.Background(), "provider-a", backend.URL)
	}
	entry = cache.entries["provider-a"]
	if !entry.healthy || entry.consecutiveSuccesses != providerHealthyThreshold || entry.consecutiveFailures != 0 {
		t.Fatalf("recovered entry = %+v", entry)
	}
}
