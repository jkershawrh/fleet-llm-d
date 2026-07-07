package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

// FleetEvent represents an event in the fleet system.
type FleetEvent struct {
	Type      string
	Payload   interface{}
	Timestamp time.Time
	Source    string
}

// EventHandler is a function that handles a FleetEvent.
type EventHandler func(ctx context.Context, event FleetEvent) error

// EventPublisher defines the interface for publishing and subscribing to fleet events.
type EventPublisher interface {
	Publish(ctx context.Context, event FleetEvent) error
	Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error
}

// subscription holds a handler and the event types it is interested in.
type subscription struct {
	eventTypes map[string]bool
	handler    EventHandler
}

// inMemoryEventPublisher is a thread-safe in-memory implementation of EventPublisher.
type inMemoryEventPublisher struct {
	mu            sync.Mutex
	events        []FleetEvent
	subscriptions []subscription
}

// NewEventPublisher returns a new EventPublisher backed by an in-memory store.
func NewEventPublisher() EventPublisher {
	return &inMemoryEventPublisher{
		events:        make([]FleetEvent, 0),
		subscriptions: make([]subscription, 0),
	}
}

// Publish stores the event and synchronously calls all matching subscribers.
func (p *inMemoryEventPublisher) Publish(ctx context.Context, event FleetEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, event)

	for _, sub := range p.subscriptions {
		if sub.eventTypes[event.Type] {
			if err := sub.handler(ctx, event); err != nil {
				return fmt.Errorf("handler error: %w", err)
			}
		}
	}
	return nil
}

// Subscribe registers a handler that will be called for events matching the given types.
func (p *inMemoryEventPublisher) Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	typeSet := make(map[string]bool, len(eventTypes))
	for _, t := range eventTypes {
		typeSet[t] = true
	}

	p.subscriptions = append(p.subscriptions, subscription{
		eventTypes: typeSet,
		handler:    handler,
	})
	return nil
}

// LedgerAwarePublisher publishes events to both the event bus AND the immutable ledger.
type LedgerAwarePublisher struct {
	inner    EventPublisher
	recorder *ledger.FleetRecorder
}

// NewLedgerAwarePublisher creates a publisher that records to both the event bus and the ARE ledger.
func NewLedgerAwarePublisher(inner EventPublisher, recorder *ledger.FleetRecorder) *LedgerAwarePublisher {
	return &LedgerAwarePublisher{inner: inner, recorder: recorder}
}

func (p *LedgerAwarePublisher) Publish(ctx context.Context, event FleetEvent) error {
	// Always publish to the inner event bus first.
	if err := p.inner.Publish(ctx, event); err != nil {
		return err
	}
	// Best-effort: record to the ARE immutable ledger.
	content, err := json.Marshal(event.Payload)
	if err != nil {
		return nil // Don't fail the publish for ledger serialization issues.
	}
	_, _ = p.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:        event.Type,
		Content:     content,
		ContentType: "application/json",
		SourceID:    event.Source,
	})
	return nil
}

func (p *LedgerAwarePublisher) Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error {
	return p.inner.Subscribe(ctx, eventTypes, handler)
}
