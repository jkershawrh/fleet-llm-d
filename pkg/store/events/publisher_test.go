package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

func TestPublish(t *testing.T) {
	tests := []struct {
		name  string
		event FleetEvent
	}{
		{
			name: "publish a fleet event",
			event: FleetEvent{
				Type:      "model.deployed",
				Payload:   map[string]string{"model": "llama-3"},
				Timestamp: time.Now(),
				Source:    "test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := NewEventPublisher()
			err := publisher.Publish(context.Background(), tt.event)
			if err != nil {
				t.Errorf("Publish() error = %v, want nil", err)
			}
		})
	}
}

func TestFleetEventCloudEventContract(t *testing.T) {
	expiry := time.Date(2026, 7, 13, 12, 5, 0, 0, time.UTC)
	event := FleetEvent{
		ID: "event-1", Type: "fleet.operation.verified", Source: "urn:fleet:test", Subject: "operation/op-1",
		Timestamp: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC), CorrelationID: "corr-1",
		CausationID: "event-0", Tenant: "tenant-a", Zone: "us-east", TraceParent: "00-trace-parent",
		ExpiresAt: &expiry, Payload: map[string]string{"state": "VERIFIED"},
	}
	envelope, err := event.CloudEvent()
	if err != nil {
		t.Fatal(err)
	}
	if envelope.SpecVersion != "1.0" || envelope.ID != "event-1" || envelope.CorrelationID != "corr-1" {
		t.Fatalf("unexpected CloudEvent envelope: %+v", envelope)
	}
	if envelope.Expiry != expiry.Format(time.RFC3339Nano) {
		t.Fatalf("expiry = %q", envelope.Expiry)
	}
}

func TestLedgerAwarePublisherFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "ledger unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	inner := NewEventPublisher()
	delivered := false
	_ = inner.Subscribe(context.Background(), []string{"fleet.scale"}, func(context.Context, FleetEvent) error {
		delivered = true
		return nil
	})
	publisher := NewLedgerAwarePublisher(inner, ledger.NewFleetRecorder(ledger.NewHTTPLedgerClient(server.URL), "fleet-controller", "fleet-llm-d"))
	err := publisher.Publish(context.Background(), FleetEvent{ID: "event-1", Type: "fleet.scale", Source: "fleet-controller", Payload: map[string]int{"replicas": 2}})
	if err == nil {
		t.Fatal("required ledger failure must fail publication")
	}
	if delivered {
		t.Fatal("event bus delivery happened before required ledger evidence")
	}
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name       string
		eventTypes []string
		handler    EventHandler
	}{
		{
			name:       "subscribe to event types with a handler",
			eventTypes: []string{"model.deployed", "model.scaled"},
			handler: func(ctx context.Context, event FleetEvent) error {
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := NewEventPublisher()
			err := publisher.Subscribe(context.Background(), tt.eventTypes, tt.handler)
			if err != nil {
				t.Errorf("Subscribe() error = %v, want nil", err)
			}
		})
	}
}
