package controller

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

// stubSolver is a minimal ConstraintSolver for testing that returns a single
// decision per call.
type stubSolver struct{}

func (s *stubSolver) Solve(_ context.Context, pool v1alpha1.FleetInferencePoolSpec, clusters []solver.ClusterInfo, _ v1alpha1.PlacementPolicySpec) ([]solver.PlacementDecision, error) {
	decisions := make([]solver.PlacementDecision, 0, len(clusters))
	for _, c := range clusters {
		decisions = append(decisions, solver.PlacementDecision{
			ClusterID: c.ID,
			Replicas:  1,
			Score:     1.0,
		})
	}
	return decisions, nil
}

// newTestReconciler creates a Reconciler with a stub solver and a static
// cluster list for testing.
func newTestReconciler() *Reconciler {
	r := NewReconciler(&stubSolver{}, func(_ context.Context) ([]solver.ClusterInfo, error) {
		return []solver.ClusterInfo{
			{ID: "cluster-1", Name: "test-cluster"},
		}, nil
	})
	r.SetActualClusterObserver(func(_ context.Context, _ v1alpha1.FleetInferencePoolSpec, desired []string) ([]string, error) {
		return append([]string(nil), desired...), nil
	})
	return r
}

func TestCRDWatcher_PollsForResources(t *testing.T) {
	reconciler := newTestReconciler()

	poolList := k8sPoolList{
		Items: []k8sPoolItem{
			{
				Metadata: k8sMetadata{
					Name:            "test-pool",
					Namespace:       "default",
					ResourceVersion: "1",
				},
				Spec: v1alpha1.FleetInferencePoolSpec{
					Model: v1alpha1.ModelSpec{
						Name:   "llama-3-70b",
						Source: "registry.example.com/llama-3-70b",
					},
					Placement: v1alpha1.PlacementRef{
						PolicyRef: "default-policy",
					},
					Serving: v1alpha1.ServingSpec{
						InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
							Spec: v1alpha1.InferencePoolTemplateSpec{
								TargetPorts: []int{8080},
							},
						},
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the expected URL path.
		expectedPath := "/apis/fleet.llm-d.ai/v1alpha1/namespaces/default/fleetinferencepools"
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: got %q, want %q", r.URL.Path, expectedPath)
		}
		// Verify Authorization header.
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("unexpected Authorization header: got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(poolList)
	}))
	defer srv.Close()

	w := &CRDWatcher{
		apiServer:    srv.URL,
		namespace:    "default",
		token:        "test-token",
		reconciler:   reconciler,
		pollInterval: 100 * time.Millisecond,
		httpClient:   srv.Client(),
		lastSeen:     make(map[string]v1alpha1.FleetInferencePoolSpec),
	}

	ctx := context.Background()
	if err := w.pollOnce(ctx); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if !w.Ready() {
		t.Fatal("watcher must be ready after a successful Kubernetes API poll")
	}

	// Verify the pool was reconciled.
	state, err := reconciler.GetPoolState("llama-3-70b")
	if err != nil {
		t.Fatalf("expected pool to be reconciled, but GetPoolState returned error: %v", err)
	}
	if state.Phase != v1alpha1.FleetPhaseRunning {
		t.Errorf("expected phase Running, got %s", state.Phase)
	}
}

func TestCRDWatcher_HandlesEmptyResponse(t *testing.T) {
	reconciler := newTestReconciler()

	poolList := k8sPoolList{Items: []k8sPoolItem{}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(poolList)
	}))
	defer srv.Close()

	w := &CRDWatcher{
		apiServer:    srv.URL,
		namespace:    "default",
		token:        "test-token",
		reconciler:   reconciler,
		pollInterval: 100 * time.Millisecond,
		httpClient:   srv.Client(),
		lastSeen:     make(map[string]v1alpha1.FleetInferencePoolSpec),
	}

	ctx := context.Background()
	if err := w.pollOnce(ctx); err != nil {
		t.Fatalf("pollOnce returned error for empty response: %v", err)
	}

	pools := reconciler.ListPools()
	if len(pools) != 0 {
		t.Errorf("expected 0 pools after empty response, got %d", len(pools))
	}
}

func TestNewCRDWatcher_VerifiesTLSByDefault(t *testing.T) {
	w := NewCRDWatcher("https://kubernetes.example", "default", "token", newTestReconciler())
	transport, ok := w.httpClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		t.Fatal("watcher transport has no TLS configuration")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification must be enabled by default")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("minimum TLS version = %d, want TLS 1.3", transport.TLSClientConfig.MinVersion)
	}
}

func TestCRDWatcher_FetchesReferencedPlacementPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/fleet.llm-d.ai/v1alpha1/namespaces/default/placementpolicies/prod" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(k8sPlacementPolicy{Spec: v1alpha1.PlacementPolicySpec{
			Constraints: []v1alpha1.PlacementConstraint{{Type: "region", Rule: "us-east-1"}},
		}})
	}))
	defer srv.Close()
	w := &CRDWatcher{apiServer: srv.URL, namespace: "default", token: "token", httpClient: srv.Client()}
	policy, err := w.getPlacementPolicy(context.Background(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Constraints) != 1 || policy.Constraints[0].Rule != "us-east-1" {
		t.Fatalf("unexpected policy: %#v", policy)
	}
}

func TestCRDWatcher_HandlesAPIError(t *testing.T) {
	reconciler := newTestReconciler()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := &CRDWatcher{
		apiServer:    srv.URL,
		namespace:    "default",
		token:        "test-token",
		reconciler:   reconciler,
		pollInterval: 100 * time.Millisecond,
		httpClient:   srv.Client(),
		lastSeen:     make(map[string]v1alpha1.FleetInferencePoolSpec),
	}

	ctx := context.Background()
	err := w.pollOnce(ctx)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if w.Ready() {
		t.Fatal("watcher must not be ready after a failed Kubernetes API poll")
	}

	// The error message should mention the unexpected status code.
	if got := err.Error(); !contains(got, "500") {
		t.Errorf("expected error to contain status code 500, got: %s", got)
	}
}

func TestCRDWatcher_DetectsDeletion(t *testing.T) {
	reconciler := newTestReconciler()

	// Pre-populate lastSeen so the watcher thinks a pool existed before.
	poolSpec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   "old-model",
			Source: "registry.example.com/old-model",
		},
		Placement: v1alpha1.PlacementRef{PolicyRef: "default"},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{TargetPorts: []int{8080}},
			},
		},
	}

	// Also add the pool to the reconciler state so DeletePool succeeds.
	reconciler.ReconcilePool(context.Background(), poolSpec)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty list -- the old pool is gone.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(k8sPoolList{Items: []k8sPoolItem{}})
	}))
	defer srv.Close()

	w := &CRDWatcher{
		apiServer:    srv.URL,
		namespace:    "default",
		token:        "test-token",
		reconciler:   reconciler,
		pollInterval: 100 * time.Millisecond,
		httpClient:   srv.Client(),
		lastSeen: map[string]v1alpha1.FleetInferencePoolSpec{
			"old-model": poolSpec,
		},
	}

	ctx := context.Background()
	if err := w.pollOnce(ctx); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}

	// The pool should have been deleted from the reconciler.
	_, err := reconciler.GetPoolState("old-model")
	if err == nil {
		t.Error("expected pool to be deleted, but GetPoolState returned no error")
	}
}

func TestCRDWatcher_RetriesFailedAddition(t *testing.T) {
	poolList := k8sPoolList{Items: []k8sPoolItem{{
		Metadata: k8sMetadata{Name: "retry-pool", Namespace: "default", ResourceVersion: "1"},
		Spec: v1alpha1.FleetInferencePoolSpec{
			Model:     v1alpha1.ModelSpec{Name: "retry-model", Source: "registry.example.com/retry"},
			Placement: v1alpha1.PlacementRef{PolicyRef: "missing-policy"},
		},
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis/fleet.llm-d.ai/v1alpha1/namespaces/default/placementpolicies/missing-policy" {
			http.Error(w, "policy unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(poolList)
	}))
	defer srv.Close()

	reconciler := newTestReconciler()
	w := NewCRDWatcher(srv.URL, "default", "token", reconciler)
	w.httpClient = srv.Client()
	for attempt := 0; attempt < 2; attempt++ {
		if err := w.pollOnce(context.Background()); err != nil {
			t.Fatalf("poll %d returned transport error: %v", attempt+1, err)
		}
		if _, acknowledged := w.lastSeen["retry-pool"]; acknowledged {
			t.Fatalf("failed addition was acknowledged on attempt %d", attempt+1)
		}
	}
}

func TestCRDWatcher_RetriesFailedDeletion(t *testing.T) {
	poolSpec := v1alpha1.FleetInferencePoolSpec{Model: v1alpha1.ModelSpec{Name: "missing-model"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(k8sPoolList{})
	}))
	defer srv.Close()

	w := &CRDWatcher{
		apiServer:  srv.URL,
		namespace:  "default",
		token:      "token",
		reconciler: newTestReconciler(),
		httpClient: srv.Client(),
		lastSeen:   map[string]v1alpha1.FleetInferencePoolSpec{"missing-pool": poolSpec},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := w.pollOnce(context.Background()); err != nil {
			t.Fatalf("poll %d returned transport error: %v", attempt+1, err)
		}
		if _, pending := w.lastSeen["missing-pool"]; !pending {
			t.Fatalf("failed deletion was forgotten on attempt %d", attempt+1)
		}
	}
}

func TestCRDWatcher_StartAndStop(t *testing.T) {
	reconciler := newTestReconciler()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(k8sPoolList{Items: []k8sPoolItem{}})
	}))
	defer srv.Close()

	w := &CRDWatcher{
		apiServer:    srv.URL,
		namespace:    "default",
		token:        "test-token",
		reconciler:   reconciler,
		pollInterval: 50 * time.Millisecond,
		httpClient:   srv.Client(),
		lastSeen:     make(map[string]v1alpha1.FleetInferencePoolSpec),
	}

	ctx, cancel := context.WithCancel(context.Background())

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	// Wait enough time for the initial poll plus at least one ticker poll.
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Give the goroutine time to exit.
	time.Sleep(50 * time.Millisecond)

	// Should have been called at least twice (initial + ticker).
	if callCount < 2 {
		t.Errorf("expected at least 2 polls, got %d", callCount)
	}
}

// contains checks if s contains substr (avoids importing strings in test).
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
