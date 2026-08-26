package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestAgentIngestionRoutes(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	// Pre-register the cluster so agent status reports are accepted.
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{
		ID: "spoke-1", Name: "spoke-1", Region: "us-east", Status: "Running",
	}); err != nil {
		t.Fatal(err)
	}

	status := postAgentJSON(t, mux, "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"spoke-1","region":"us-east",
		"phase":"Running","gpu_available":4,"gpu_total":8,"healthy":true,
		"health_url":"http://spoke-1.example/readyz",
		"inference_url":"http://spoke-1.example",
		"routing_endpoint":"https://spoke-1.example",
		"metrics_endpoint":"https://metrics.spoke-1.example",
		"tls_server_name":"spoke-1.example"
	}`)
	if status.Code != http.StatusOK {
		t.Fatalf("status report returned %d: %s", status.Code, status.Body.String())
	}
	record, err := fc.ClusterRepo.Get(context.Background(), "spoke-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.GPUAvailable != 4 || record.GPUTotal != 8 || record.Labels["health_url"] == "" || record.Labels["inference_url"] == "" {
		t.Fatalf("unexpected cluster record: %+v", record)
	}
	if record.Labels["fleet.llm-d.ai/egress-address"] != "https://spoke-1.example" || record.Labels["fleet.llm-d.ai/metrics-endpoint"] != "https://metrics.spoke-1.example" {
		t.Fatalf("routing endpoint labels = %#v", record.Labels)
	}
	updated := postAgentJSON(t, mux, "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"spoke-1","phase":"Degraded",
		"gpu_available":0,"gpu_total":8,"healthy":false
	}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("status update returned %d: %s", updated.Code, updated.Body.String())
	}
	record, err = fc.ClusterRepo.Get(context.Background(), "spoke-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "Degraded" || record.Labels["health_url"] != "" || record.Labels["inference_url"] != "" {
		t.Fatalf("unexpected updated cluster record: %+v", record)
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

func TestAgentStatusDoesNotCreateAfterRepositoryReadFailure(t *testing.T) {
	fc := newTestFleetController(t)
	repo := &failingClusterRepo{getErr: errors.New("database unavailable")}
	fc.ClusterRepo = repo

	response := postAgentJSON(t, fc.SetupRoutes("control"), "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"spoke-1","phase":"Running",
		"gpu_available":0,"gpu_total":0,"healthy":true
	}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status report returned %d: %s", response.Code, response.Body.String())
	}
	if repo.createCalls != 0 {
		t.Fatalf("Create called %d times after a read failure", repo.createCalls)
	}
}

func TestAgentStatusRejectsUnregisteredCluster(t *testing.T) {
	fc := newTestFleetController(t)

	response := postAgentJSON(t, fc.SetupRoutes("control"), "/api/v1/agent/status", `{
		"cluster_id":"unregistered-1","name":"rogue","phase":"Running",
		"gpu_available":1,"gpu_total":2,"healthy":true
	}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unregistered agent status should return 403, got %d: %s", response.Code, response.Body.String())
	}
}

func TestLeaderGateRejectsAgentWritesOnStandby(t *testing.T) {
	fc := newTestFleetController(t)
	fc.ConfigureLeaderElection(controller.NewLeaderElector("http://127.0.0.1:1", "test", "standby"))

	response := postAgentJSON(t, fc.leaderGate(fc.SetupRoutes("control")), "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"spoke-1","phase":"Running",
		"gpu_available":0,"gpu_total":0,"healthy":true
	}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("standby status report returned %d: %s", response.Code, response.Body.String())
	}
}

func TestAgentEventPublishesStructuredPayload(t *testing.T) {
	fc := newTestFleetController(t)
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

type failingClusterRepo struct {
	getErr      error
	createCalls int
}

func (r *failingClusterRepo) Create(context.Context, postgres.ClusterRecord) error {
	r.createCalls++
	return nil
}
func (r *failingClusterRepo) Get(context.Context, string) (*postgres.ClusterRecord, error) {
	return nil, r.getErr
}
func (r *failingClusterRepo) List(context.Context) ([]postgres.ClusterRecord, error) { return nil, nil }
func (r *failingClusterRepo) Update(context.Context, postgres.ClusterRecord) error   { return nil }
func (r *failingClusterRepo) Delete(context.Context, string) error                   { return nil }
