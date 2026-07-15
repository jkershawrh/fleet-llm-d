package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/events"
)

func TestAgentIngestionRoutes(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	mux := fc.SetupRoutes("control")

	status := postAgentJSON(t, mux, "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"spoke-1","region":"us-east",
		"phase":"Running","gpu_available":4,"gpu_total":8,"healthy":true,
		"health_url":"http://spoke-1.example/healthz"
	}`)
	if status.Code != http.StatusOK {
		t.Fatalf("status report returned %d: %s", status.Code, status.Body.String())
	}
	record, err := fc.ClusterRepo.Get(context.Background(), "spoke-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.GPUAvailable != 4 || record.GPUTotal != 8 || record.Labels["health_url"] == "" {
		t.Fatalf("unexpected cluster record: %+v", record)
	}

	metrics := postAgentJSON(t, mux, "/api/v1/agent/metrics", `{
		"cluster_id":"spoke-1","throughput_tps":42.5,"ttft_p50_ms":25,
		"ttft_p99_ms":80,"queue_depth":3,"gpu_utilization":75,"kv_cache_hit_rate":0.9
	}`)
	if metrics.Code != http.StatusAccepted {
		t.Fatalf("metrics report returned %d: %s", metrics.Code, metrics.Body.String())
	}
	collected, err := fc.MetricsCollector.CollectCluster(context.Background(), "spoke-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(collected.Pools) != 1 || collected.Pools[0].Throughput_TPS != 42.5 || collected.Pools[0].TTFT_P50_Ms != 25 {
		t.Fatalf("unexpected collected metrics: %+v", collected)
	}
}

func TestAgentEventPublishesStructuredPayload(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	received := make(chan events.FleetEvent, 1)
	if err := fc.EventPublisher.Subscribe(context.Background(), []string{"fleet.agent.event"}, func(_ context.Context, event events.FleetEvent) error {
		received <- event
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	response := postAgentJSON(t, fc.SetupRoutes("control"), "/api/v1/agent/events", `{
		"cluster_id":"spoke-1","event":{"ClusterUnhealthy":{"reason":"probe failed"}}
	}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("event report returned %d: %s", response.Code, response.Body.String())
	}
	select {
	case event := <-received:
		if event.Subject != "spoke-1" {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatal("expected event to be published")
	}
}

func postAgentJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
