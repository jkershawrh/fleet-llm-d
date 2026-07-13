package events

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

// FleetEvent represents an event in the fleet system.
type FleetEvent struct {
	ID            string
	Type          string
	Payload       interface{}
	Timestamp     time.Time
	Source        string
	Subject       string
	DataSchema    string
	Tenant        string
	Zone          string
	CorrelationID string
	CausationID   string
	TraceParent   string
	ExpiresAt     *time.Time
}

// CloudEventEnvelope is the canonical CloudEvents 1.0 wire contract used by
// fleet producers and consumers. Extension names intentionally use lowercase
// CloudEvents attribute syntax.
type CloudEventEnvelope struct {
	SpecVersion     string      `json:"specversion"`
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	Source          string      `json:"source"`
	Subject         string      `json:"subject,omitempty"`
	Time            time.Time   `json:"time"`
	DataContentType string      `json:"datacontenttype"`
	DataSchema      string      `json:"dataschema,omitempty"`
	CorrelationID   string      `json:"correlationid,omitempty"`
	CausationID     string      `json:"causationid,omitempty"`
	Tenant          string      `json:"tenant,omitempty"`
	Zone            string      `json:"zone,omitempty"`
	TraceParent     string      `json:"traceparent,omitempty"`
	Expiry          string      `json:"expiry,omitempty"`
	Data            interface{} `json:"data"`
}

func (event FleetEvent) CloudEvent() (CloudEventEnvelope, error) {
	if event.Type == "" {
		return CloudEventEnvelope{}, fmt.Errorf("event type is required")
	}
	if event.Source == "" {
		event.Source = "urn:fleet-llm-d:controller"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ID == "" {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return CloudEventEnvelope{}, fmt.Errorf("marshal event data: %w", err)
		}
		digest := sha256.Sum256(append([]byte(event.Type+"\x00"+event.Source+"\x00"+event.Timestamp.UTC().Format(time.RFC3339Nano)+"\x00"), payload...))
		event.ID = fmt.Sprintf("fleet-%x", digest[:])
	}
	expiry := ""
	if event.ExpiresAt != nil {
		expiry = event.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return CloudEventEnvelope{
		SpecVersion: "1.0", ID: event.ID, Type: event.Type, Source: event.Source,
		Subject: event.Subject, Time: event.Timestamp.UTC(), DataContentType: "application/json",
		DataSchema: event.DataSchema, CorrelationID: event.CorrelationID, CausationID: event.CausationID,
		Tenant: event.Tenant, Zone: event.Zone, TraceParent: event.TraceParent, Expiry: expiry, Data: event.Payload,
	}, nil
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
	envelope, err := event.CloudEvent()
	if err != nil {
		return err
	}
	content, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal CloudEvent: %w", err)
	}
	if p.recorder == nil {
		return fmt.Errorf("required ARE ledger recorder is not configured")
	}
	if _, err := p.recorder.RecordDecision(ctx, ledger.FleetDecision{
		Type:           event.Type,
		CorrelationID:  event.CorrelationID,
		IdempotencyKey: envelope.ID,
		Content:        content,
		ContentType:    "application/cloudevents+json",
		SourceID:       event.Source,
	}); err != nil {
		return fmt.Errorf("record required event evidence: %w", err)
	}
	return p.inner.Publish(ctx, event)
}

func (p *LedgerAwarePublisher) Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error {
	return p.inner.Subscribe(ctx, eventTypes, handler)
}
