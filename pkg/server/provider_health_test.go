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

	cache, err := NewProviderHealthCache(map[string]string{"arena-xeon6": backend.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	cache.ttl = time.Minute
	if healthy, configured := cache.Healthy(context.Background(), "arena-xeon6"); !configured || healthy {
		t.Fatalf("failed probe = healthy %v, configured %v", healthy, configured)
	}
	status = http.StatusOK
	if healthy, _ := cache.Healthy(context.Background(), "arena-xeon6"); healthy {
		t.Fatal("cached failed probe unexpectedly became healthy")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 cached probe", requests)
	}
	cache.mu.Lock()
	cache.entries["arena-xeon6"] = providerHealthEntry{healthy: false, checkedAt: time.Now().Add(-2 * time.Minute)}
	cache.mu.Unlock()
	if healthy, _ := cache.Healthy(context.Background(), "arena-xeon6"); !healthy {
		t.Fatal("provider did not recover after successful probe")
	}
}

func TestProviderHealthCacheLeavesUnconfiguredProviderToRepositoryHealth(t *testing.T) {
	cache, err := NewProviderHealthCache(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if healthy, configured := cache.Healthy(context.Background(), "oberon-cpu"); configured || healthy {
		t.Fatalf("unconfigured provider = healthy %v, configured %v", healthy, configured)
	}
}
