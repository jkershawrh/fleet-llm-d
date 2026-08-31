package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPEventPublisher_Publish(t *testing.T) {
	var received httpEventPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pub := NewHTTPEventPublisher(server.URL)
	err := pub.Publish(context.Background(), FleetEvent{
		Type:      "fleet.test.event",
		Payload:   map[string]string{"key": "value"},
		Timestamp: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if received.Type != "fleet.test.event" {
		t.Errorf("expected type fleet.test.event, got %s", received.Type)
	}
	if received.Source != "test" {
		t.Errorf("expected source test, got %s", received.Source)
	}
}

func TestHTTPEventPublisher_FallbackOnFailure(t *testing.T) {
	pub := NewHTTPEventPublisher("http://localhost:1/nonexistent")

	var localReceived bool
	pub.Subscribe(context.Background(), []string{"fleet.test"}, func(ctx context.Context, event FleetEvent) error {
		localReceived = true
		return nil
	})

	err := pub.Publish(context.Background(), FleetEvent{
		Type:      "fleet.test",
		Payload:   "data",
		Timestamp: time.Now(),
		Source:    "test",
	})
	if err == nil {
		t.Fatal("publish must surface remote delivery failure for outbox retry")
	}
	if !localReceived {
		t.Error("local subscriber should have received event despite HTTP failure")
	}
}

func TestHTTPEventPublisher_Subscribe(t *testing.T) {
	pub := NewHTTPEventPublisher("http://localhost:1/nonexistent")

	var count int
	pub.Subscribe(context.Background(), []string{"fleet.a"}, func(ctx context.Context, event FleetEvent) error {
		count++
		return nil
	})

	pub.Publish(context.Background(), FleetEvent{Type: "fleet.a", Timestamp: time.Now()})
	pub.Publish(context.Background(), FleetEvent{Type: "fleet.b", Timestamp: time.Now()})
	pub.Publish(context.Background(), FleetEvent{Type: "fleet.a", Timestamp: time.Now()})

	if count != 2 {
		t.Errorf("expected 2 deliveries for fleet.a, got %d", count)
	}
}
