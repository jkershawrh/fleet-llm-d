package events

import (
	"context"
	"testing"
	"time"
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
