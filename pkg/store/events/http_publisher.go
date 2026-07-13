package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

type httpEventPayload = CloudEventEnvelope

// Publish sends the event to the HTTP endpoint and to local subscribers.
// Local delivery is attempted even when the remote sink is unavailable, but
// the remote failure is returned so an authoritative outbox can retry it.
func (p *HTTPEventPublisher) Publish(ctx context.Context, event FleetEvent) error {
	envelope, err := event.CloudEvent()
	if err != nil {
		return err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	var deliveryErr error
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(body))
	if err != nil {
		deliveryErr = fmt.Errorf("create event request: %w", err)
	} else {
		req.Header.Set("Content-Type", "application/cloudevents+json")
		resp, err := p.http.Do(req)
		if err != nil {
			deliveryErr = fmt.Errorf("deliver event: %w", err)
		} else {
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				deliveryErr = fmt.Errorf("event endpoint returned %d", resp.StatusCode)
			}
		}
	}

	if localErr := p.inner.Publish(ctx, event); localErr != nil {
		if deliveryErr != nil {
			return fmt.Errorf("remote delivery failed (%v); local delivery failed: %w", deliveryErr, localErr)
		}
		return localErr
	}
	return deliveryErr
}

// Subscribe registers a local event handler.
func (p *HTTPEventPublisher) Subscribe(ctx context.Context, eventTypes []string, handler EventHandler) error {
	return p.inner.Subscribe(ctx, eventTypes, handler)
}
