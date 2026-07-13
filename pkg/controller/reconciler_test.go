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

func newObservedTestReconciler() *Reconciler {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)
	r.SetActualClusterObserver(func(_ context.Context, _ v1alpha1.FleetInferencePoolSpec, desired []string) ([]string, error) {
		return append([]string(nil), desired...), nil
	})
	return r
}

func TestReconcilePool_NewPool(t *testing.T) {
	r := newObservedTestReconciler()

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

func TestReconcilePool_DoesNotClaimRunningWithoutObservedState(t *testing.T) {
	r := NewReconciler(solver.NewConstraintSolver(), testClusterLister)
	if err := r.ReconcilePool(context.Background(), testPool("unobserved")); err != nil {
		t.Fatal(err)
	}
	state, err := r.GetPoolState("unobserved")
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != v1alpha1.FleetPhasePlacing {
		t.Fatalf("phase = %s, want %s until provider state is observed", state.Phase, v1alpha1.FleetPhasePlacing)
	}
	if len(state.ActualClusters) != 0 {
		t.Fatalf("actual clusters must not be inferred from desired placement: %v", state.ActualClusters)
	}
}

type policyCapturingSolver struct {
	policy v1alpha1.PlacementPolicySpec
}

func (s *policyCapturingSolver) Solve(_ context.Context, _ v1alpha1.FleetInferencePoolSpec, _ []solver.ClusterInfo, policy v1alpha1.PlacementPolicySpec) ([]solver.PlacementDecision, error) {
	s.policy = policy
	return []solver.PlacementDecision{{ClusterID: "cluster-1", Replicas: 1}}, nil
}

func TestReconcilePool_ResolvesReferencedPlacementPolicy(t *testing.T) {
	capturing := &policyCapturingSolver{}
	r := NewReconciler(capturing, testClusterLister)
	r.SetPlacementPolicyResolver(func(_ context.Context, ref string) (v1alpha1.PlacementPolicySpec, error) {
		if ref != "default" {
			t.Fatalf("policy ref = %q, want default", ref)
		}
		return v1alpha1.PlacementPolicySpec{Constraints: []v1alpha1.PlacementConstraint{{Type: "region", Rule: "us-east-1"}}}, nil
	})
	if err := r.ReconcilePool(context.Background(), testPool("policy-aware")); err != nil {
		t.Fatal(err)
	}
	if len(capturing.policy.Constraints) != 1 || capturing.policy.Constraints[0].Rule != "us-east-1" {
		t.Fatalf("solver did not receive resolved policy: %#v", capturing.policy)
	}
}

func TestReconcilePool_UpdatePool(t *testing.T) {
	r := newObservedTestReconciler()

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
	r := newObservedTestReconciler()

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
	r := newObservedTestReconciler()

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
	r := newObservedTestReconciler()
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
	r := newObservedTestReconciler()

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

func TestReconcilePool_DoesNotBlockReads(t *testing.T) {
	slowLister := func(ctx context.Context) ([]solver.ClusterInfo, error) {
		time.Sleep(500 * time.Millisecond)
		return []solver.ClusterInfo{
			{ID: "c1", Name: "c1", Region: "us", GPUCapacity: solver.GPUCapacity{Available: 4, Total: 8}},
		}, nil
	}

	r := NewReconciler(solver.NewConstraintSolver(), slowLister)

	// Seed a pool so ListPools has something to return
	fastLister := func(ctx context.Context) ([]solver.ClusterInfo, error) {
		return []solver.ClusterInfo{
			{ID: "c1", Name: "c1", Region: "us", GPUCapacity: solver.GPUCapacity{Available: 4, Total: 8}},
		}, nil
	}
	fastReconciler := NewReconciler(solver.NewConstraintSolver(), fastLister)
	pool := v1alpha1.FleetInferencePoolSpec{
		Model:     v1alpha1.ModelSpec{Name: "seed-model", Source: "test"},
		Placement: v1alpha1.PlacementRef{PolicyRef: "default", MaxClusters: 1},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{TargetPorts: []int{8000}},
			},
		},
	}
	fastReconciler.ReconcilePool(context.Background(), pool)

	// Now use the slow lister reconciler
	r.ReconcilePool(context.Background(), pool) // seed it

	// Start a slow reconcile in background
	go r.ReconcilePool(context.Background(), v1alpha1.FleetInferencePoolSpec{
		Model:     v1alpha1.ModelSpec{Name: "slow-model", Source: "test"},
		Placement: v1alpha1.PlacementRef{PolicyRef: "default", MaxClusters: 1},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{TargetPorts: []int{8000}},
			},
		},
	})
	time.Sleep(50 * time.Millisecond) // let it start

	// ListPools should NOT block
	done := make(chan struct{})
	go func() {
		r.ListPools()
		close(done)
	}()

	select {
	case <-done:
		// pass — ListPools returned quickly
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ListPools blocked while ReconcilePool was fetching clusters")
	}
}

func TestReconcilePool_OnChangeCalledOutsideLock(t *testing.T) {
	lister := func(ctx context.Context) ([]solver.ClusterInfo, error) {
		return []solver.ClusterInfo{
			{ID: "c1", Name: "c1", Region: "us", GPUCapacity: solver.GPUCapacity{Available: 4, Total: 8}},
		}, nil
	}

	r := NewReconciler(solver.NewConstraintSolver(), lister)

	var callbackCalled bool
	r.SetOnChange(func(pool *FleetPoolState) {
		// If this is called inside the lock, trying to call ListPools would deadlock.
		// We test by calling ListPools from the callback.
		_ = r.ListPools()
		callbackCalled = true
	})

	pool := v1alpha1.FleetInferencePoolSpec{
		Model:     v1alpha1.ModelSpec{Name: "test-model", Source: "test"},
		Placement: v1alpha1.PlacementRef{PolicyRef: "default", MaxClusters: 1},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{TargetPorts: []int{8000}},
			},
		},
	}

	err := r.ReconcilePool(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !callbackCalled {
		t.Fatal("onChange callback was not called")
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
