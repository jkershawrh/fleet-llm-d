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
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	mux := fc.SetupRoutes("control")

	status := postAgentJSON(t, mux, "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"spoke-1","region":"us-east",
		"phase":"Running","gpu_available":4,"gpu_total":8,"healthy":true,
		"health_url":"http://spoke-1.example/readyz",
		"inference_url":"http://spoke-1.example"
	}`)
	if status.Code != http.StatusCreated {
		t.Fatalf("status report returned %d: %s", status.Code, status.Body.String())
	}
	record, err := fc.ClusterRepo.Get(context.Background(), "spoke-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.GPUAvailable != 4 || record.GPUTotal != 8 || record.Labels["health_url"] == "" || record.Labels["inference_url"] == "" {
		t.Fatalf("unexpected cluster record: %+v", record)
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
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
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

func TestAgentStatusCompletesConcurrentCreateAsUpdate(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	repo := &racingClusterRepo{record: postgres.ClusterRecord{ID: "spoke-1", Name: "old"}}
	fc.ClusterRepo = repo

	response := postAgentJSON(t, fc.SetupRoutes("control"), "/api/v1/agent/status", `{
		"cluster_id":"spoke-1","name":"new","phase":"Running",
		"gpu_available":1,"gpu_total":2,"healthy":true
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status report returned %d: %s", response.Code, response.Body.String())
	}
	if repo.record.Name != "new" || repo.updateCalls != 1 {
		t.Fatalf("concurrent create was not completed as an update: %+v", repo)
	}
}

func TestLeaderGateRejectsAgentWritesOnStandby(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
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

type racingClusterRepo struct {
	record      postgres.ClusterRecord
	getCalls    int
	updateCalls int
}

func (r *racingClusterRepo) Create(context.Context, postgres.ClusterRecord) error {
	return postgres.ErrClusterAlreadyExists
}
func (r *racingClusterRepo) Get(context.Context, string) (*postgres.ClusterRecord, error) {
	r.getCalls++
	if r.getCalls == 1 {
		return nil, postgres.ErrClusterNotFound
	}
	copy := r.record
	return &copy, nil
}
func (r *racingClusterRepo) List(context.Context) ([]postgres.ClusterRecord, error) { return nil, nil }
func (r *racingClusterRepo) Update(_ context.Context, record postgres.ClusterRecord) error {
	r.updateCalls++
	r.record = record
	return nil
}
func (r *racingClusterRepo) Delete(context.Context, string) error { return nil }
