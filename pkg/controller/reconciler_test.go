package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

// testPool returns a basic FleetInferencePoolSpec for testing.
func testPool(name string) v1alpha1.FleetInferencePoolSpec {
	return v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   name,
			Source: "hf://test/" + name,
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "default",
			MaxClusters: 3,
		},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8080},
				},
			},
		},
	}
}

// testClusterLister returns a cluster lister that provides two test clusters.
func testClusterLister(ctx context.Context) ([]solver.ClusterInfo, error) {
	return []solver.ClusterInfo{
		{
			ID:     "cluster-1",
			Name:   "us-east",
			Region: "us-east-1",
			GPUCapacity: solver.GPUCapacity{
				Available: 4,
				Total:     8,
				Types:     []string{"A100"},
			},
		},
		{
			ID:     "cluster-2",
			Name:   "eu-west",
			Region: "eu-west-1",
			GPUCapacity: solver.GPUCapacity{
				Available: 2,
				Total:     4,
				Types:     []string{"A100"},
			},
		},
	}, nil
}

func TestReconcilePool_NewPool(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)

	pool := testPool("llama-3")
	if err := r.ReconcilePool(context.Background(), pool); err != nil {
		t.Fatalf("ReconcilePool returned unexpected error: %v", err)
	}

	state, err := r.GetPoolState("llama-3")
	if err != nil {
		t.Fatalf("GetPoolState returned unexpected error: %v", err)
	}

	if state.Phase != v1alpha1.FleetPhaseRunning {
		t.Errorf("expected phase %q, got %q", v1alpha1.FleetPhaseRunning, state.Phase)
	}

	if len(state.DesiredClusters) == 0 {
		t.Error("expected DesiredClusters to be non-empty")
	}

	if time.Since(state.LastReconciled) > 5*time.Second {
		t.Errorf("expected LastReconciled to be recent, got %v", state.LastReconciled)
	}
}

func TestReconcilePool_UpdatePool(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)

	pool := testPool("llama-3")
	if err := r.ReconcilePool(context.Background(), pool); err != nil {
		t.Fatalf("initial ReconcilePool returned unexpected error: %v", err)
	}

	// Reconcile the same pool again (update scenario).
	if err := r.ReconcilePool(context.Background(), pool); err != nil {
		t.Fatalf("update ReconcilePool returned unexpected error: %v", err)
	}

	state, err := r.GetPoolState("llama-3")
	if err != nil {
		t.Fatalf("GetPoolState returned unexpected error: %v", err)
	}

	if state.Phase != v1alpha1.FleetPhaseRunning {
		t.Errorf("expected phase %q after update, got %q", v1alpha1.FleetPhaseRunning, state.Phase)
	}

	if len(state.DesiredClusters) == 0 {
		t.Error("expected DesiredClusters to be populated after update")
	}
}

func TestDeletePool(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)

	pool := testPool("llama-3")
	if err := r.ReconcilePool(context.Background(), pool); err != nil {
		t.Fatalf("ReconcilePool returned unexpected error: %v", err)
	}

	if err := r.DeletePool(context.Background(), "llama-3"); err != nil {
		t.Fatalf("DeletePool returned unexpected error: %v", err)
	}

	// Verify pool is gone.
	if _, err := r.GetPoolState("llama-3"); err == nil {
		t.Error("expected error from GetPoolState after deletion, got nil")
	}

	// Verify deleting a non-existent pool returns an error.
	if err := r.DeletePool(context.Background(), "nonexistent"); err == nil {
		t.Error("expected error from DeletePool for non-existent pool, got nil")
	}
}

func TestListPools(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)

	pool1 := testPool("llama-3")
	pool2 := testPool("mistral-7b")

	if err := r.ReconcilePool(context.Background(), pool1); err != nil {
		t.Fatalf("ReconcilePool(llama-3) returned unexpected error: %v", err)
	}
	if err := r.ReconcilePool(context.Background(), pool2); err != nil {
		t.Fatalf("ReconcilePool(mistral-7b) returned unexpected error: %v", err)
	}

	pools := r.ListPools()
	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}

	names := make(map[string]bool)
	for _, p := range pools {
		names[p.Name] = true
	}
	if !names["llama-3"] {
		t.Error("expected pool llama-3 in list")
	}
	if !names["mistral-7b"] {
		t.Error("expected pool mistral-7b in list")
	}
}

func TestWatchEndpoint_Added(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)
	srv := httptest.NewServer(r.WatchEndpoint())
	defer srv.Close()

	event := watchEvent{
		Type:   "ADDED",
		Object: testPool("llama-3"),
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}

	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	state, err := r.GetPoolState("llama-3")
	if err != nil {
		t.Fatalf("GetPoolState returned unexpected error: %v", err)
	}
	if state.Phase != v1alpha1.FleetPhaseRunning {
		t.Errorf("expected phase %q, got %q", v1alpha1.FleetPhaseRunning, state.Phase)
	}
}

func TestWatchEndpoint_Deleted(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)

	// Pre-populate a pool so we can delete it.
	pool := testPool("llama-3")
	if err := r.ReconcilePool(context.Background(), pool); err != nil {
		t.Fatalf("ReconcilePool returned unexpected error: %v", err)
	}

	srv := httptest.NewServer(r.WatchEndpoint())
	defer srv.Close()

	event := watchEvent{
		Type:   "DELETED",
		Object: pool,
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}

	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify pool is gone.
	if _, err := r.GetPoolState("llama-3"); err == nil {
		t.Error("expected error from GetPoolState after deletion, got nil")
	}
}

func TestWatchEndpoint_BadJSON(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)
	srv := httptest.NewServer(r.WatchEndpoint())
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json", strings.NewReader("{invalid"))
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}
