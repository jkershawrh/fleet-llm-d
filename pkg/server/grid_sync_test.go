package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestGridSyncLoop_NilTranslator_ReturnsImmediately(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	fc.GridCRDTranslator = nil

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fc.runGridSyncLoop(ctx)
}

func TestGridSyncLoop_TranslatesAllClusters(t *testing.T) {
	var mu sync.Mutex
	var appliedPaths []string

	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		appliedPaths = append(appliedPaths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer mockAPI.Close()

	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	fc.GridCRDTranslator = routing.NewGridCRDTranslator(mockAPI.URL, "fleet-llm-d", "", "test-grid")
	fc.SWIMSyncAdapter = nil

	ctx := context.Background()
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{ID: "oberon", Name: "oberon", Region: "us-east-1", Status: "Running"})
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{ID: "arena", Name: "arena", Region: "us-east-1", Status: "Running"})
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{ID: "brutus", Name: "brutus", Region: "us-east-1", Status: "Running"})

	fc.runGridSyncCycle(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(appliedPaths) < 3 {
		t.Fatalf("expected at least 3 GridSite applies, got %d: %v", len(appliedPaths), appliedPaths)
	}
}

func TestGridSyncLoop_TranslatesAllPools(t *testing.T) {
	var mu sync.Mutex
	var appliedPaths []string

	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		appliedPaths = append(appliedPaths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer mockAPI.Close()

	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	fc.GridCRDTranslator = routing.NewGridCRDTranslator(mockAPI.URL, "fleet-llm-d", "", "test-grid")
	fc.SWIMSyncAdapter = nil

	ctx := context.Background()
	fc.PoolRepo.Create(ctx, postgres.FleetPoolRecord{ID: "granite-2b", Name: "granite-2b", ModelName: "granite-2b", Status: "Active"})
	fc.PoolRepo.Create(ctx, postgres.FleetPoolRecord{ID: "granite-8b", Name: "granite-8b", ModelName: "granite-8b", Status: "Active"})

	fc.runGridSyncCycle(ctx)

	mu.Lock()
	defer mu.Unlock()
	hasProvider := false
	for _, p := range appliedPaths {
		if len(p) > 0 {
			hasProvider = true
		}
	}
	if !hasProvider {
		t.Fatal("expected InferenceProvider applies for pools")
	}
}

func TestGridSyncLoop_SWIMSyncUpdatesClusterStatus(t *testing.T) {
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis/grid.praxis-proxy.io/v1alpha1/gridsites" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[{"metadata":{"name":"arena"},"status":{"phase":"Unreachable"}}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockAPI.Close()

	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	fc.GridCRDTranslator = routing.NewGridCRDTranslator(mockAPI.URL, "fleet-llm-d", "", "test-grid")
	fc.SWIMSyncAdapter = client.NewSWIMSyncAdapter(mockAPI.URL, "", fc.ClusterRepo)

	ctx := context.Background()
	fc.ClusterRepo.Create(ctx, postgres.ClusterRecord{ID: "arena", Name: "arena", Region: "us-east-1", Status: "Running"})

	fc.runGridSyncCycle(ctx)

	record, err := fc.ClusterRepo.Get(ctx, "arena")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "Degraded" {
		t.Fatalf("expected arena status Degraded after SWIM sync, got %q", record.Status)
	}
}

func TestGridSyncLoop_PublishesGridSyncedEvent(t *testing.T) {
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockAPI.Close()

	fc, err := NewFleetControllerWithLedgerConfig(
		ledger.Config{Mode: ledger.ModeMemory},
		"http://vllm", "http://ovms", "", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	fc.GridCRDTranslator = routing.NewGridCRDTranslator(mockAPI.URL, "fleet-llm-d", "", "test-grid")
	fc.SWIMSyncAdapter = nil

	var received []events.FleetEvent
	var mu sync.Mutex
	fc.EventPublisher.Subscribe(context.Background(), []string{events.EventGridSynced}, func(ctx context.Context, event events.FleetEvent) error {
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		return nil
	})

	fc.runGridSyncCycle(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected fleet.grid.synced event to be published")
	}
	if received[0].Type != events.EventGridSynced {
		t.Fatalf("expected event type %q, got %q", events.EventGridSynced, received[0].Type)
	}
}
