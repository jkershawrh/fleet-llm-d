package modelplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelPlaneWatcher_LastClustersDoesNotBlockDuringPoll(t *testing.T) {
	// Create a slow mock server that takes 500ms per request
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "inferenceclusters"):
			w.Write([]byte(`{"items":[{"name":"c1","region":"us"}]}`))
		case strings.Contains(r.URL.Path, "modeldeployments"):
			w.Write([]byte(`{"items":[]}`))
		case strings.Contains(r.URL.Path, "modelendpoints"):
			w.Write([]byte(`{"items":[]}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer slowServer.Close()

	watcher := NewModelPlaneWatcher(slowServer.URL, "default", "")

	// Trigger a poll in the background (will take ~1.5s for 3 requests)
	go func() {
		watcher.PollOnce(context.Background())
	}()
	time.Sleep(100 * time.Millisecond) // let poll start

	// LastClusters should return quickly, not block for 1.5s
	done := make(chan struct{})
	go func() {
		watcher.LastClusters()
		close(done)
	}()

	select {
	case <-done:
		// pass -- returned quickly
	case <-time.After(300 * time.Millisecond):
		t.Fatal("LastClusters blocked while pollOnce was running")
	}
}
