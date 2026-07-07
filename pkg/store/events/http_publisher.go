package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// HTTPEventPublisher publishes fleet events to an HTTP endpoint (webhook receiver,
// Kafka REST Proxy, or any HTTP-compatible event sink) in addition to local subscribers.
type HTTPEventPublisher struct {
	endpoint string
	http     *http.Client
	inner    *inMemoryEventPublisher
}

// NewHTTPEventPublisher creates a publisher that posts events to the given HTTP endpoint
// and also delivers them to local subscribers via the in-memory publisher.
func NewHTTPEventPublisher(endpoint string) *HTTPEventPublisher {
	return &HTTPEventPublisher{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 5 * time.Second},
		inner:    &inMemoryEventPublisher{},
	}
}

type httpEventPayload struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp string      `json:"timestamp"`
	Source    string      `json:"source"`
}

// Publish sends the event to the HTTP endpoint and to local subscribers.
// HTTP delivery failure is logged but does not block local delivery.
func (p *HTTPEventPublisher) Publish(ctx context.Context, event FleetEvent) error {
	body, err := json.Marshal(httpEventPayload{
		Type:      event.Type,
		Payload:   event.Payload,
		Timestamp: event.Timestamp.Format(time.RFC3339),
		Source:    event.Source,
	})
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(body))
	if err != nil {
		log.Printf("http event publish: create request failed: %v", err)
	} else {
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.http.Do(req)
		if err != nil {
			log.Printf("http event publish: delivery failed: %v", err)
		} else {
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				log.Printf("http event publish: endpoint returned %d", resp.StatusCode)
			}
		}
	}

	return p.inner.Publish(ctx, event)
}

// Subscribe registers a local event handler.
func (p *HTTPEventPublisher) Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error {
	return p.inner.Subscribe(ctx, eventTypes, handler)
}
