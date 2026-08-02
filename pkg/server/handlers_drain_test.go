package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestDrainRunningCluster(t *testing.T) {
	fc := newTestController()
	ctx := context.Background()
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{
		ID:     "c1",
		Name:   "Cluster 1",
		Status: postgres.ClusterStatusRunning,
	})

	req := httptest.NewRequest("POST", "/api/v1/clusters/c1/drain", nil)
	req.SetPathValue("id", "c1")
	w := httptest.NewRecorder()
	fc.handleDrainCluster(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	record, _ := fc.ClusterRepo.Get(ctx, "c1")
	if record.Status != postgres.ClusterStatusDraining {
		t.Fatalf("expected Draining, got %s", record.Status)
	}
	if record.Labels["drain_started_at"] == "" {
		t.Fatal("expected drain_started_at label")
	}
}

func TestDrainDegradedCluster(t *testing.T) {
	fc := newTestController()
	ctx := context.Background()
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{
		ID:     "c1",
		Name:   "Cluster 1",
		Status: postgres.ClusterStatusDegraded,
	})

	req := httptest.NewRequest("POST", "/api/v1/clusters/c1/drain", nil)
	req.SetPathValue("id", "c1")
	w := httptest.NewRecorder()
	fc.handleDrainCluster(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	record, _ := fc.ClusterRepo.Get(ctx, "c1")
	if record.Status != postgres.ClusterStatusDraining {
		t.Fatalf("expected Draining, got %s", record.Status)
	}
}

func TestDrainAlreadyDraining(t *testing.T) {
	fc := newTestController()
	ctx := context.Background()
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{
		ID:     "c1",
		Status: postgres.ClusterStatusDraining,
	})

	req := httptest.NewRequest("POST", "/api/v1/clusters/c1/drain", nil)
	req.SetPathValue("id", "c1")
	w := httptest.NewRecorder()
	fc.handleDrainCluster(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestDrainNonExistent(t *testing.T) {
	fc := newTestController()

	req := httptest.NewRequest("POST", "/api/v1/clusters/missing/drain", nil)
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()
	fc.handleDrainCluster(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestActivateDrainedCluster(t *testing.T) {
	fc := newTestController()
	ctx := context.Background()
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{
		ID:     "c1",
		Status: postgres.ClusterStatusDrained,
		Labels: map[string]string{"drain_started_at": "2026-07-30T00:00:00Z"},
	})

	req := httptest.NewRequest("POST", "/api/v1/clusters/c1/activate", nil)
	req.SetPathValue("id", "c1")
	w := httptest.NewRecorder()
	fc.handleActivateCluster(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	record, _ := fc.ClusterRepo.Get(ctx, "c1")
	if record.Status != postgres.ClusterStatusRunning {
		t.Fatalf("expected Running, got %s", record.Status)
	}
	if _, ok := record.Labels["drain_started_at"]; ok {
		t.Fatal("drain_started_at should be removed after activate")
	}
}

func TestAgentDoesNotOverwriteDraining(t *testing.T) {
	record := &postgres.ClusterRecord{
		ID:     "c1",
		Status: postgres.ClusterStatusDraining,
		Labels: make(map[string]string),
	}
	report := agentStatusReport{
		ClusterID:    "c1",
		Name:         "c1",
		GPUAvailable: 8,
		GPUTotal:     8,
		Healthy:      true,
	}
	applyAgentStatus(record, report, "Running")
	if record.Status != postgres.ClusterStatusDraining {
		t.Fatalf("expected Draining preserved, got %s", record.Status)
	}
	if record.GPUAvailable != 8 {
		t.Fatal("GPU data should still be updated")
	}
}

func newTestController() *FleetController {
	return &FleetController{
		ClusterRepo:   postgres.NewInMemoryClusterRepository(),
		FleetRecorder: nil,
	}
}
