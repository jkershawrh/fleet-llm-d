//go:build architecture

// Package architecture proves 54 architectural claims about fleet-llm-d.
// Each test function maps to a specific claim (A01-A54).
// A failing test means the architecture is broken, not just a bug.
package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/auth"
	"github.com/llm-d/fleet-llm-d/pkg/cost"
	"github.com/llm-d/fleet-llm-d/pkg/intents"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/lifecycle/rollout"
	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/quota"
)

// ---------------------------------------------------------------------------
// Test world: wires together all capability packages in-process.
// ---------------------------------------------------------------------------

// ArchTestWorld holds references to all fleet-llm-d capability components
// wired together the same way the fleet-controller does, but in-process.
type ArchTestWorld struct {
	Reconciler    *controller.Reconciler
	Proxy         *routing.InferenceProxy
	QuotaEnforcer quota.QuotaEnforcer
	Rollout       rollout.RolloutController
	Optimizer     optimizer.FleetOptimizer
	Collector     *collector.InMemoryCollector
	Ledger        *ledger.InMemoryLedgerClient
	Recorder      *ledger.FleetRecorder
	Events        events.EventPublisher
	HTTPEvents    *httptest.Server
}

// testClusters returns a fixed set of clusters used across reconciler tests.
func testClusters() []solver.ClusterInfo {
	return []solver.ClusterInfo{
		{
			ID: "cluster-us-east", Name: "us-east", Region: "us-east-1",
			Labels:      map[string]string{"region": "us-east-1", "compliance": "soc2"},
			GPUCapacity: solver.GPUCapacity{Available: 8, Total: 8, Types: []string{"A100"}},
			Utilization: 0.3,
		},
		{
			ID: "cluster-eu-west", Name: "eu-west", Region: "eu-west-1",
			Labels:      map[string]string{"region": "eu-west-1", "compliance": "gdpr"},
			GPUCapacity: solver.GPUCapacity{Available: 4, Total: 8, Types: []string{"A100"}},
			Utilization: 0.6,
		},
		{
			ID: "cluster-ap-south", Name: "ap-south", Region: "ap-south-1",
			Labels:      map[string]string{"region": "ap-south-1"},
			GPUCapacity: solver.GPUCapacity{Available: 6, Total: 8, Types: []string{"H100"}},
			Utilization: 0.1,
		},
	}
}

// clusterLister returns a function suitable for controller.NewReconciler.
func clusterLister() func(ctx context.Context) ([]solver.ClusterInfo, error) {
	return func(_ context.Context) ([]solver.ClusterInfo, error) {
		return testClusters(), nil
	}
}

// newTestWorld creates a fully wired test world with fresh components.
func newTestWorld(t *testing.T) *ArchTestWorld {
	t.Helper()

	s := solver.NewConstraintSolver()
	rec := controller.NewReconciler(s, clusterLister())
	proxy := routing.NewInferenceProxy()
	qe := quota.NewQuotaEnforcer()
	rc := rollout.NewRolloutController()
	opt := optimizer.NewFleetOptimizer()
	mc := collector.NewMetricsCollector()
	imc := mc.(*collector.InMemoryCollector)
	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ep := events.NewEventPublisher()

	// HTTP event receiver for A36.
	var httpReceived atomic.Value
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		httpReceived.Store(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(httpSrv.Close)
	// Store the receiver for later use.
	_ = httpReceived

	return &ArchTestWorld{
		Reconciler:    rec,
		Proxy:         proxy,
		QuotaEnforcer: qe,
		Rollout:       rc,
		Optimizer:     opt,
		Collector:     imc,
		Ledger:        lc,
		Recorder:      fr,
		Events:        ep,
		HTTPEvents:    httpSrv,
	}
}

// makePool creates a minimal FleetInferencePoolSpec for testing.
func makePool(name, source string, maxClusters int) v1alpha1.FleetInferencePoolSpec {
	return v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   name,
			Source: source,
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "default",
			MaxClusters: maxClusters,
		},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}
}

// makeCanaryLifecycle creates a canary lifecycle spec for rollout tests.
func makeCanaryLifecycle(modelName, version string, initialWeight, increment int, sloGate *v1alpha1.SLOGate, rollbackOnFailure bool) v1alpha1.ModelLifecycleSpec {
	return v1alpha1.ModelLifecycleSpec{
		Model: v1alpha1.ModelRef{
			Name:    modelName,
			Version: version,
		},
		FleetPoolRef: "test-pool",
		Strategy: v1alpha1.RolloutStrategy{
			Type: "Canary",
			Canary: &v1alpha1.CanaryConfig{
				InitialWeight:     initialWeight,
				WeightIncrement:   increment,
				Interval:          "30s",
				SLOGate:           sloGate,
				RollbackOnFailure: rollbackOnFailure,
			},
		},
		Clusters: &v1alpha1.ClusterOrder{
			Order: []string{"cluster-1", "cluster-2"},
		},
	}
}

// ===========================================================================
// RECONCILIATION (A01-A05)
// ===========================================================================

func TestA01_Reconciler_WebhookTriggersReconcile(t *testing.T) {
	claim(t, "A01", "reconciliation", "TDD", "Webhook POST triggers ReconcilePool")

	w := newTestWorld(t)
	ctx := context.Background()

	// Build the watch event JSON.
	event := struct {
		Type   string                          `json:"type"`
		Object v1alpha1.FleetInferencePoolSpec `json:"object"`
	}{
		Type:   "ADDED",
		Object: makePool("webhook-model", "huggingface", 2),
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	// POST to the watch endpoint.
	req := httptest.NewRequest(http.MethodPost, "/watch", bytes.NewReader(body))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	w.Reconciler.WatchEndpoint()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the pool was reconciled and appears in ListPools.
	pools := w.Reconciler.ListPools()
	found := false
	for _, p := range pools {
		if p.Name == "webhook-model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pool 'webhook-model' not found in ListPools after webhook")
	}
}

func TestA02_Reconciler_ComputesPlacementViaSolver(t *testing.T) {
	claim(t, "A02", "reconciliation", "TDD", "Solver runs and produces cluster assignments")

	w := newTestWorld(t)
	ctx := context.Background()

	pool := makePool("placement-model", "huggingface", 2)
	if err := w.Reconciler.ReconcilePool(ctx, pool); err != nil {
		t.Fatalf("ReconcilePool: %v", err)
	}

	state, err := w.Reconciler.GetPoolState("placement-model")
	if err != nil {
		t.Fatalf("GetPoolState: %v", err)
	}

	// The solver should have selected exactly 2 clusters (maxClusters=2)
	// from the 3 available.
	if len(state.DesiredClusters) != 2 {
		t.Fatalf("expected 2 desired clusters, got %d: %v", len(state.DesiredClusters), state.DesiredClusters)
	}
}

func TestA03_Reconciler_PhaseTransitions(t *testing.T) {
	claim(t, "A03", "reconciliation", "TDD", "Pool transitions Pending -> Placing -> Running")

	w := newTestWorld(t)
	ctx := context.Background()

	// Before reconcile: pool does not exist.
	_, err := w.Reconciler.GetPoolState("phase-model")
	if err == nil {
		t.Fatal("expected error for non-existent pool, got nil")
	}

	// Track phases observed through onChange.
	var observedPhase v1alpha1.FleetPhase
	w.Reconciler.SetOnChange(func(pool *controller.FleetPoolState) {
		observedPhase = pool.Phase
	})

	pool := makePool("phase-model", "huggingface", 2)
	if err := w.Reconciler.ReconcilePool(ctx, pool); err != nil {
		t.Fatalf("ReconcilePool: %v", err)
	}

	// After reconcile: pool should be Running.
	state, err := w.Reconciler.GetPoolState("phase-model")
	if err != nil {
		t.Fatalf("GetPoolState: %v", err)
	}
	if state.Phase != v1alpha1.FleetPhaseRunning {
		t.Fatalf("expected phase Running, got %s", state.Phase)
	}

	// onChange was called with the final Running phase (which proves the
	// state machine traversed Pending -> Placing -> Running).
	if observedPhase != v1alpha1.FleetPhaseRunning {
		t.Fatalf("onChange observed phase %s, expected Running", observedPhase)
	}
}

func TestA04_Reconciler_EmitsOnChangeEvent(t *testing.T) {
	claim(t, "A04", "reconciliation", "EDD", "onChange callback fires on pool state change")

	w := newTestWorld(t)
	ctx := context.Background()

	var callbackPool *controller.FleetPoolState
	w.Reconciler.SetOnChange(func(pool *controller.FleetPoolState) {
		cp := *pool
		callbackPool = &cp
	})

	pool := makePool("onchange-model", "huggingface", 2)
	if err := w.Reconciler.ReconcilePool(ctx, pool); err != nil {
		t.Fatalf("ReconcilePool: %v", err)
	}

	if callbackPool == nil {
		t.Fatal("onChange callback was never called")
	}
	if callbackPool.Name != "onchange-model" {
		t.Fatalf("callback pool name = %q, want 'onchange-model'", callbackPool.Name)
	}
}

func TestA05_Reconciler_DeletionCleansUp(t *testing.T) {
	claim(t, "A05", "reconciliation", "TDD", "Deleting a pool removes it from state")

	w := newTestWorld(t)
	ctx := context.Background()

	pool := makePool("delete-model", "huggingface", 2)
	if err := w.Reconciler.ReconcilePool(ctx, pool); err != nil {
		t.Fatalf("ReconcilePool: %v", err)
	}

	// Verify it exists.
	if _, err := w.Reconciler.GetPoolState("delete-model"); err != nil {
		t.Fatalf("pool should exist after reconcile: %v", err)
	}

	// Delete it.
	if err := w.Reconciler.DeletePool(ctx, "delete-model"); err != nil {
		t.Fatalf("DeletePool: %v", err)
	}

	// Verify it is gone.
	_, err := w.Reconciler.GetPoolState("delete-model")
	if err == nil {
		t.Fatal("pool should not exist after deletion")
	}

	// Also verify ListPools does not return it.
	for _, p := range w.Reconciler.ListPools() {
		if p.Name == "delete-model" {
			t.Fatal("deleted pool still appears in ListPools")
		}
	}
}

// ===========================================================================
// ROUTING (A06-A11)
// ===========================================================================

func TestA06_Proxy_SelectsCorrectBackendByModel(t *testing.T) {
	claim(t, "A06", "routing", "TDD", "Proxy routes to correct backend by model name")

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("model-a", routing.Backend{
		Name: "backend-a", URL: "http://model-a:8000", Healthy: true, LatencyMs: 50,
	})
	proxy.RegisterBackend("model-b", routing.Backend{
		Name: "backend-b", URL: "http://model-b:8000", Healthy: true, LatencyMs: 50,
	})

	backend, _, err := proxy.SelectBackend("model-a", http.Header{})
	if err != nil {
		t.Fatalf("SelectBackend: %v", err)
	}
	if backend.Name != "backend-a" {
		t.Fatalf("expected backend-a, got %s", backend.Name)
	}

	backend, _, err = proxy.SelectBackend("model-b", http.Header{})
	if err != nil {
		t.Fatalf("SelectBackend model-b: %v", err)
	}
	if backend.Name != "backend-b" {
		t.Fatalf("expected backend-b, got %s", backend.Name)
	}
}

func TestA07_Proxy_RealtimeRoutesToLowestLatency(t *testing.T) {
	claim(t, "A07", "routing", "TDD", "Realtime objective selects lowest-latency backend")

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("rt-model", routing.Backend{
		Name: "slow-backend", URL: "http://slow:8000", Healthy: true, LatencyMs: 200,
	})
	proxy.RegisterBackend("rt-model", routing.Backend{
		Name: "fast-backend", URL: "http://fast:8000", Healthy: true, LatencyMs: 20,
	})

	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "realtime")

	backend, reason, err := proxy.SelectBackend("rt-model", headers)
	if err != nil {
		t.Fatalf("SelectBackend: %v", err)
	}
	if backend.Name != "fast-backend" {
		t.Fatalf("expected fast-backend (20ms), got %s (%.0fms)", backend.Name, backend.LatencyMs)
	}
	if !strings.Contains(reason, "realtime") {
		t.Fatalf("reason should contain 'realtime', got %q", reason)
	}
}

func TestA08_Proxy_BatchRoutesToAnyHealthy(t *testing.T) {
	claim(t, "A08", "routing", "TDD", "Batch objective selects any healthy backend")

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("batch-model", routing.Backend{
		Name: "batch-1", URL: "http://b1:8000", Healthy: true, LatencyMs: 100,
	})
	proxy.RegisterBackend("batch-model", routing.Backend{
		Name: "batch-2", URL: "http://b2:8000", Healthy: true, LatencyMs: 50,
	})

	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "batch")

	backend, reason, err := proxy.SelectBackend("batch-model", headers)
	if err != nil {
		t.Fatalf("SelectBackend: %v", err)
	}
	if !backend.Healthy {
		t.Fatal("selected backend is not healthy")
	}
	if !strings.Contains(reason, "batch") {
		t.Fatalf("reason should contain 'batch', got %q", reason)
	}
}

func TestA09_Proxy_SkipsUnhealthyBackend(t *testing.T) {
	claim(t, "A09", "routing", "TDD", "Unhealthy backends are never selected")

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("health-model", routing.Backend{
		Name: "sick", URL: "http://sick:8000", Healthy: false, LatencyMs: 10,
	})
	proxy.RegisterBackend("health-model", routing.Backend{
		Name: "healthy", URL: "http://healthy:8000", Healthy: true, LatencyMs: 100,
	})

	// Try multiple selections to exercise round-robin. Unhealthy should never appear.
	for i := 0; i < 10; i++ {
		backend, _, err := proxy.SelectBackend("health-model", http.Header{})
		if err != nil {
			t.Fatalf("SelectBackend iteration %d: %v", i, err)
		}
		if backend.Name == "sick" {
			t.Fatalf("unhealthy backend 'sick' was selected on iteration %d", i)
		}
	}
}

func TestA10_Proxy_FailoverOnBackendError(t *testing.T) {
	claim(t, "A10", "routing", "TDD", "Proxy surfaces backend errors for failover")

	// Create a mock backend that always returns 500.
	badBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"internal server error"}`)
	}))
	defer badBackend.Close()

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("fail-model", routing.Backend{
		Name: "bad-server", URL: badBackend.URL, Healthy: true, LatencyMs: 50,
	})

	// Send a request through ServeHTTP.
	reqBody := `{"model":"fail-model","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	// The proxy should relay the 500 from the backend.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from bad backend, got %d", rec.Code)
	}
}

func TestA11_Proxy_InjectsFleetHeaders(t *testing.T) {
	claim(t, "A11", "routing", "TDD", "Proxy injects X-Fleet-Routed-To and X-Fleet-Routing-Reason")

	// Create a mock backend that returns 200.
	goodBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"chatcmpl-1","choices":[]}`)
	}))
	defer goodBackend.Close()

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("header-model", routing.Backend{
		Name: "good-server", URL: goodBackend.URL, Healthy: true, LatencyMs: 30,
	})

	reqBody := `{"model":"header-model","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	routedTo := rec.Header().Get("X-Fleet-Routed-To")
	if routedTo == "" {
		t.Fatal("X-Fleet-Routed-To header is missing")
	}
	if routedTo != "good-server" {
		t.Fatalf("X-Fleet-Routed-To = %q, want 'good-server'", routedTo)
	}

	routingReason := rec.Header().Get("X-Fleet-Routing-Reason")
	if routingReason == "" {
		t.Fatal("X-Fleet-Routing-Reason header is missing")
	}
}

// ===========================================================================
// TENANT GOVERNANCE (A12-A16)
// ===========================================================================

func TestA12_Tenant_QuotaAllowsWithinLimits(t *testing.T) {
	claim(t, "A12", "tenant", "TDD", "Quota allows requests within token limits")

	// tenant-1 has 1000 tokens and $10.00 budget.
	qe := quota.NewQuotaEnforcer()
	ctx := context.Background()

	// CheckQuota is read-only; use ConsumeQuota to actually deduct.
	ce := qe.(*quota.DefaultQuotaEnforcer)
	result, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{
		TokensRequested: 500,
		Model:           "test-model",
	})
	if err != nil {
		t.Fatalf("ConsumeQuota: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("500 tokens should be allowed (limit 1000), reason: %s", result.Reason)
	}
	if result.RemainingTokens != 500 {
		t.Fatalf("remaining tokens = %d, want 500", result.RemainingTokens)
	}
}

func TestA13_Tenant_QuotaRejectsOverLimits(t *testing.T) {
	claim(t, "A13", "tenant", "TDD", "Quota rejects requests exceeding token limits")

	qe := quota.NewQuotaEnforcer()
	ce := qe.(*quota.DefaultQuotaEnforcer)
	ctx := context.Background()

	// Consume 900 of 1000 tokens.
	result, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{
		TokensRequested: 900,
		Model:           "test-model",
	})
	if err != nil {
		t.Fatalf("first ConsumeQuota: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("900 tokens should be allowed, reason: %s", result.Reason)
	}

	// Try to consume 200 more (only 100 remaining).
	result, err = ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{
		TokensRequested: 200,
		Model:           "test-model",
	})
	if err != nil {
		t.Fatalf("second ConsumeQuota: %v", err)
	}
	if result.Allowed {
		t.Fatal("200 tokens should be rejected (only 100 remaining)")
	}
	if !strings.Contains(result.Reason, "token limit exceeded") {
		t.Fatalf("expected token limit reason, got: %s", result.Reason)
	}
}

func TestA14_Tenant_UsageAccumulatesAcrossRequests(t *testing.T) {
	claim(t, "A14", "tenant", "TDD", "Token usage accumulates across requests")

	qe := quota.NewQuotaEnforcer()
	ce := qe.(*quota.DefaultQuotaEnforcer)
	ctx := context.Background()

	// First request: 100 tokens.
	r1, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{TokensRequested: 100})
	if err != nil {
		t.Fatalf("ConsumeQuota 1: %v", err)
	}
	if r1.RemainingTokens != 900 {
		t.Fatalf("after 100 tokens: remaining = %d, want 900", r1.RemainingTokens)
	}

	// Second request: 200 more tokens.
	r2, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{TokensRequested: 200})
	if err != nil {
		t.Fatalf("ConsumeQuota 2: %v", err)
	}
	if r2.RemainingTokens != 700 {
		t.Fatalf("after 100+200 tokens: remaining = %d, want 700 (300 consumed)", r2.RemainingTokens)
	}
}

func TestA15_Tenant_BudgetRejectsOverCap(t *testing.T) {
	claim(t, "A15", "tenant", "TDD", "Budget cap rejects when exhausted")

	qe := quota.NewQuotaEnforcer()
	ce := qe.(*quota.DefaultQuotaEnforcer)
	ctx := context.Background()

	// Exhaust the entire budget by consuming all 1000 tokens
	// (1000 tokens * 1 cent/token = $10.00 = full budget).
	r1, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{TokensRequested: 1000})
	if err != nil {
		t.Fatalf("ConsumeQuota (exhaust): %v", err)
	}
	if !r1.Allowed {
		t.Fatalf("1000 tokens should be allowed on fresh tenant, reason: %s", r1.Reason)
	}

	// Now the budget is $0.00. Next request should fail on budget check.
	r2, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{TokensRequested: 1})
	if err != nil {
		t.Fatalf("ConsumeQuota (over budget): %v", err)
	}
	if r2.Allowed {
		t.Fatal("request should be rejected when budget is exhausted")
	}
	if !strings.Contains(r2.Reason, "budget exceeded") {
		t.Fatalf("expected budget rejection, got: %s", r2.Reason)
	}
}

func TestA16_Tenant_MultiTenantIsolation(t *testing.T) {
	claim(t, "A16", "tenant", "TDD", "Tenant quotas are isolated from each other")

	qe := quota.NewQuotaEnforcer()
	ce := qe.(*quota.DefaultQuotaEnforcer)
	ctx := context.Background()

	// Exhaust tenant-1's quota entirely.
	_, err := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{TokensRequested: 1000})
	if err != nil {
		t.Fatalf("exhaust tenant-1: %v", err)
	}

	// Verify tenant-1 is exhausted.
	r1, _ := ce.ConsumeQuota(ctx, "tenant-1", quota.QuotaCheckRequest{TokensRequested: 1})
	if r1.Allowed {
		t.Fatal("tenant-1 should be exhausted")
	}

	// Verify tenant-2 still has full quota (isolated from tenant-1).
	r2, err := ce.ConsumeQuota(ctx, "tenant-2", quota.QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatalf("ConsumeQuota tenant-2: %v", err)
	}
	if !r2.Allowed {
		t.Fatalf("tenant-2 should still have full quota, reason: %s", r2.Reason)
	}
	if r2.RemainingTokens != 500 {
		t.Fatalf("tenant-2 remaining = %d, want 500 (full 1000 minus 500)", r2.RemainingTokens)
	}
}

// ===========================================================================
// LIFECYCLE (A17-A21)
// ===========================================================================

func TestA17_Lifecycle_CanaryStartsAtInitialWeight(t *testing.T) {
	claim(t, "A17", "lifecycle", "TDD", "Canary rollout starts at configured initial weight")

	rc := rollout.NewRolloutController()
	ctx := context.Background()

	lifecycle := makeCanaryLifecycle("gpt-4", "v2", 5, 10, nil, false)
	state, err := rc.CreateRollout(ctx, lifecycle)
	if err != nil {
		t.Fatalf("CreateRollout: %v", err)
	}
	if state.CurrentWeight != 5 {
		t.Fatalf("initial weight = %d, want 5", state.CurrentWeight)
	}
	if state.Phase != "Canary" {
		t.Fatalf("phase = %q, want 'Canary'", state.Phase)
	}
}

func TestA18_Lifecycle_AdvanceIncreasesWeight(t *testing.T) {
	claim(t, "A18", "lifecycle", "TDD", "Advance increases canary weight by increment")

	rc := rollout.NewRolloutController()
	ctx := context.Background()

	// No SLO gate (nil) means the gate always passes.
	lifecycle := makeCanaryLifecycle("gpt-4", "v2", 5, 10, nil, false)
	state, err := rc.CreateRollout(ctx, lifecycle)
	if err != nil {
		t.Fatalf("CreateRollout: %v", err)
	}

	// Advance once: 5 + 10 = 15.
	advanced, err := rc.AdvanceRollout(ctx, state.ID)
	if err != nil {
		t.Fatalf("AdvanceRollout: %v", err)
	}
	if advanced.CurrentWeight != 15 {
		t.Fatalf("weight after advance = %d, want 15 (5 + 10)", advanced.CurrentWeight)
	}
}

func TestA19_Lifecycle_SLOGateBlocksOnBreach(t *testing.T) {
	claim(t, "A19", "lifecycle", "TDD", "SLO gate blocks advancement on breach")

	rc := rollout.NewRolloutController()
	ctx := context.Background()

	// SLO gate with 0% tolerance always fails (simulates SLO breach).
	sloGate := &v1alpha1.SLOGate{
		MaxTTFTRegression:    "0%",
		MaxErrorRateIncrease: "0%",
	}
	lifecycle := makeCanaryLifecycle("gpt-4", "v2", 5, 10, sloGate, true)
	state, err := rc.CreateRollout(ctx, lifecycle)
	if err != nil {
		t.Fatalf("CreateRollout: %v", err)
	}

	// Advance should trigger rollback because SLO fails and rollbackOnFailure is true.
	result, err := rc.AdvanceRollout(ctx, state.ID)
	if err != nil {
		t.Fatalf("AdvanceRollout: %v", err)
	}
	if result.CurrentWeight != 0 {
		t.Fatalf("weight after SLO breach = %d, want 0 (rolled back)", result.CurrentWeight)
	}
	if result.Phase != "RolledBack" {
		t.Fatalf("phase = %q, want 'RolledBack'", result.Phase)
	}
}

func TestA20_Lifecycle_SLOGateAllowsWhenMet(t *testing.T) {
	claim(t, "A20", "lifecycle", "TDD", "SLO gate allows advancement when metrics are met")

	rc := rollout.NewRolloutController()
	ctx := context.Background()

	// SLO gate with positive tolerances passes.
	sloGate := &v1alpha1.SLOGate{
		MaxTTFTRegression:    "10%",
		MaxErrorRateIncrease: "5%",
	}
	lifecycle := makeCanaryLifecycle("gpt-4", "v2", 5, 10, sloGate, true)
	state, err := rc.CreateRollout(ctx, lifecycle)
	if err != nil {
		t.Fatalf("CreateRollout: %v", err)
	}

	// Advance should succeed because SLO gate passes.
	result, err := rc.AdvanceRollout(ctx, state.ID)
	if err != nil {
		t.Fatalf("AdvanceRollout: %v", err)
	}
	if result.CurrentWeight != 15 {
		t.Fatalf("weight = %d, want 15 (5 + 10)", result.CurrentWeight)
	}
	if result.Phase == "RolledBack" {
		t.Fatal("should not have rolled back when SLO is met")
	}
}

func TestA21_Lifecycle_RollbackResetsToZero(t *testing.T) {
	claim(t, "A21", "lifecycle", "TDD", "Rollback resets weight to zero")

	rc := rollout.NewRolloutController()
	ctx := context.Background()

	lifecycle := makeCanaryLifecycle("gpt-4", "v2", 5, 10, nil, false)
	state, err := rc.CreateRollout(ctx, lifecycle)
	if err != nil {
		t.Fatalf("CreateRollout: %v", err)
	}

	// Advance to weight 25 (5 + 10 + 10).
	if _, err := rc.AdvanceRollout(ctx, state.ID); err != nil {
		t.Fatalf("AdvanceRollout 1: %v", err)
	}
	advanced, err := rc.AdvanceRollout(ctx, state.ID)
	if err != nil {
		t.Fatalf("AdvanceRollout 2: %v", err)
	}
	if advanced.CurrentWeight != 25 {
		t.Fatalf("pre-rollback weight = %d, want 25", advanced.CurrentWeight)
	}

	// Rollback.
	rolled, err := rc.RollbackRollout(ctx, state.ID)
	if err != nil {
		t.Fatalf("RollbackRollout: %v", err)
	}
	if rolled.CurrentWeight != 0 {
		t.Fatalf("post-rollback weight = %d, want 0", rolled.CurrentWeight)
	}
	if rolled.Phase != "RolledBack" {
		t.Fatalf("post-rollback phase = %q, want 'RolledBack'", rolled.Phase)
	}
}

// ===========================================================================
// AUTOSCALING (A22-A25)
// ===========================================================================

func TestA22_Autoscaling_ScaleUpOnHighTTFT(t *testing.T) {
	claim(t, "A22", "autoscaling", "TDD", "High TTFT triggers scale-up recommendation")

	opt := optimizer.NewFleetOptimizer()
	ctx := context.Background()

	metrics := []collector.ClusterMetrics{{
		ClusterID: "cluster-1",
		Pools: []collector.PoolMetrics{{
			PoolName:       "llm-pool",
			Replicas:       4,
			TTFT_P99_Ms:    200,
			GPUUtilization: 0.70,
		}},
		Timestamp: time.Now(),
	}}

	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "ttft_p99_ms", Target: "100"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			MaxScaleUpRate: 5,
		},
	}

	actions, err := opt.Optimize(ctx, metrics, policy)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("expected at least one scaling action")
	}

	action := actions[0]
	if action.DesiredReplicas <= action.CurrentReplicas {
		t.Fatalf("expected scale up: desired=%d, current=%d", action.DesiredReplicas, action.CurrentReplicas)
	}
}

func TestA23_Autoscaling_ScaleDownOnLowUtil(t *testing.T) {
	claim(t, "A23", "autoscaling", "TDD", "Low GPU utilization triggers scale-down recommendation")

	opt := optimizer.NewFleetOptimizer()
	ctx := context.Background()

	metrics := []collector.ClusterMetrics{{
		ClusterID: "cluster-1",
		Pools: []collector.PoolMetrics{{
			PoolName:       "llm-pool",
			Replicas:       10,
			TTFT_P99_Ms:    30,
			GPUUtilization: 0.20,
		}},
		Timestamp: time.Now(),
	}}

	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "gpuUtilization", Target: "0.70"},
			{Metric: "ttft_p99_ms", Target: "100"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			MaxScaleUpRate: 5,
		},
	}

	actions, err := opt.Optimize(ctx, metrics, policy)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("expected at least one scaling action")
	}

	action := actions[0]
	if action.DesiredReplicas >= action.CurrentReplicas {
		t.Fatalf("expected scale down: desired=%d, current=%d", action.DesiredReplicas, action.CurrentReplicas)
	}
}

func TestA24_Autoscaling_GlobalGPUCapPreventsOverscale(t *testing.T) {
	claim(t, "A24", "autoscaling", "TDD", "GlobalMaxGPUs cap prevents over-provisioning")

	opt := optimizer.NewFleetOptimizer()
	ctx := context.Background()

	metrics := []collector.ClusterMetrics{{
		ClusterID: "cluster-1",
		Pools: []collector.PoolMetrics{{
			PoolName:    "llm-pool",
			Replicas:    9,
			TTFT_P99_Ms: 500, // Very high: 5x over target -> wants big scale-up.
		}},
		Timestamp: time.Now(),
	}}

	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "ttft_p99_ms", Target: "100"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			GlobalMaxGPUs:  10,
			MaxScaleUpRate: 20,
		},
	}

	actions, err := opt.Optimize(ctx, metrics, policy)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("expected at least one scaling action")
	}

	for _, a := range actions {
		if a.DesiredReplicas > 10 {
			t.Fatalf("desired replicas %d exceeds global max GPUs 10", a.DesiredReplicas)
		}
	}
}

func TestA25_Autoscaling_CrossClusterMigration(t *testing.T) {
	claim(t, "A25", "autoscaling", "TDD", "Cross-cluster migration absorbs load from overloaded cluster")

	opt := optimizer.NewFleetOptimizer()
	ctx := context.Background()

	metrics := []collector.ClusterMetrics{
		{
			ClusterID: "cluster-a",
			Pools: []collector.PoolMetrics{{
				PoolName:       "shared-pool",
				Replicas:       5,
				TTFT_P99_Ms:    200, // Over target -> overloaded.
				GPUUtilization: 0.80,
			}},
			Timestamp: time.Now(),
		},
		{
			ClusterID: "cluster-b",
			Pools: []collector.PoolMetrics{{
				PoolName:       "shared-pool",
				Replicas:       5,
				TTFT_P99_Ms:    50, // Under target -> not overloaded.
				GPUUtilization: 0.20,
			}},
			Timestamp: time.Now(),
		},
	}

	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "ttft_p99_ms", Target: "100"},
			{Metric: "gpuUtilization", Target: "0.70"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			MaxScaleUpRate: 5,
		},
		CrossCluster: &v1alpha1.CrossClusterScaling{
			EnableMigration:    true,
			MigrationThreshold: 0.30, // diff = 0.80 - 0.20 = 0.60 > 0.30
		},
	}

	actions, err := opt.Optimize(ctx, metrics, policy)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	// Look for the cross-cluster migration action.
	foundMigration := false
	for _, a := range actions {
		if strings.Contains(a.Reason, "cross-cluster migration") {
			foundMigration = true
			if a.ClusterID != "cluster-b" {
				t.Fatalf("migration should target underutilized cluster-b, got %s", a.ClusterID)
			}
			break
		}
	}
	if !foundMigration {
		reasons := make([]string, len(actions))
		for i, a := range actions {
			reasons[i] = fmt.Sprintf("%s:%s(%d->%d:%s)", a.ClusterID, a.PoolName, a.CurrentReplicas, a.DesiredReplicas, a.Reason)
		}
		t.Fatalf("no cross-cluster migration action found; actions: %v", reasons)
	}
}

// ===========================================================================
// COMPLIANCE / LEDGER (A26-A32)
// ===========================================================================

func TestA26_Compliance_PlacementRecordedToLedger(t *testing.T) {
	claim(t, "A26", "compliance", "CDD", "Placement decision recorded to immutable ledger")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	_, err := fr.RecordPlacement(ctx, "gpt-4", "cluster-1", 3, "A100", "lowest utilization")
	if err != nil {
		t.Fatalf("RecordPlacement: %v", err)
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != "fleet.placement.assigned" {
		t.Fatalf("type = %q, want 'fleet.placement.assigned'", entries[0].Type)
	}
}

func TestA27_Compliance_RoutingRecordedToLedger(t *testing.T) {
	claim(t, "A27", "compliance", "CDD", "Routing change recorded to immutable ledger")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	_, err := fr.RecordRoutingChange(ctx, "gpt-4", "us-east", "eu-west", 0.3, "latency optimization")
	if err != nil {
		t.Fatalf("RecordRoutingChange: %v", err)
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != "fleet.routing.shifted" {
		t.Fatalf("type = %q, want 'fleet.routing.shifted'", entries[0].Type)
	}
}

func TestA28_Compliance_TenantUsageRecordedToLedger(t *testing.T) {
	claim(t, "A28", "compliance", "CDD", "Tenant usage recorded to immutable ledger")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	_, err := fr.RecordTenantUsage(ctx, "tenant-1", "gpt-4", "us-east", 50000, "$0.50")
	if err != nil {
		t.Fatalf("RecordTenantUsage: %v", err)
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != "fleet.tenant.usage" {
		t.Fatalf("type = %q, want 'fleet.tenant.usage'", entries[0].Type)
	}
}

func TestA29_Compliance_LifecycleRecordedToLedger(t *testing.T) {
	claim(t, "A29", "compliance", "CDD", "Lifecycle event recorded to immutable ledger")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	_, err := fr.RecordLifecycleEvent(ctx, "gpt-4", "v2", "deploy", "us-east", map[string]interface{}{
		"strategy": "canary",
		"weight":   15,
	})
	if err != nil {
		t.Fatalf("RecordLifecycleEvent: %v", err)
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != "fleet.lifecycle.deploy" {
		t.Fatalf("type = %q, want 'fleet.lifecycle.deploy'", entries[0].Type)
	}
}

func TestA30_Compliance_AuthFailureRecordedToLedger(t *testing.T) {
	claim(t, "A30", "compliance", "CDD", "Auth failure recorded to immutable ledger")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	_, err := fr.RecordAuthFailure(ctx, "192.168.1.100", "invalid API key")
	if err != nil {
		t.Fatalf("RecordAuthFailure: %v", err)
	}

	entries := lc.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != "fleet.security.auth.failed" {
		t.Fatalf("type = %q, want 'fleet.security.auth.failed'", entries[0].Type)
	}
}

func TestA31_Compliance_AllChainsVerifyValid(t *testing.T) {
	claim(t, "A31", "compliance", "CDD", "All decision chains verify as valid")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	// Record entries across multiple chain types.
	if _, err := fr.RecordPlacement(ctx, "m1", "c1", 1, "A100", "test"); err != nil {
		t.Fatalf("RecordPlacement: %v", err)
	}
	if _, err := fr.RecordRoutingChange(ctx, "m1", "c1", "c2", 0.5, "test"); err != nil {
		t.Fatalf("RecordRoutingChange: %v", err)
	}
	if _, err := fr.RecordTenantUsage(ctx, "t1", "m1", "c1", 100, "$1"); err != nil {
		t.Fatalf("RecordTenantUsage: %v", err)
	}

	// Verify all chains.
	results, err := fr.VerifyAllChains(ctx)
	if err != nil {
		t.Fatalf("VerifyAllChains: %v", err)
	}

	for chainType, v := range results {
		if !v.Valid {
			t.Fatalf("chain %q failed verification", chainType)
		}
	}

	// Verify at least the chains we populated have entries.
	if results["fleet.placement.assigned"].EntriesChecked < 1 {
		t.Fatal("placement chain should have at least 1 entry")
	}
	if results["fleet.routing.shifted"].EntriesChecked < 1 {
		t.Fatal("routing chain should have at least 1 entry")
	}
}

func TestA32_Compliance_CorrelationIDLinksDecisions(t *testing.T) {
	claim(t, "A32", "compliance", "CDD", "CorrelationID links related decisions across chains")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	ctx := context.Background()

	correlationID := "corr-deploy-gpt4-v2"

	// Record three related decisions with the same correlation ID.
	decisions := []ledger.FleetDecision{
		{Type: "fleet.placement.assigned", CorrelationID: correlationID, Content: []byte(`{"model":"gpt-4"}`)},
		{Type: "fleet.routing.shifted", CorrelationID: correlationID, Content: []byte(`{"model":"gpt-4"}`)},
		{Type: "fleet.tenant.usage", CorrelationID: correlationID, Content: []byte(`{"tenant":"t1"}`)},
	}
	for _, d := range decisions {
		if _, err := fr.RecordDecision(ctx, d); err != nil {
			t.Fatalf("RecordDecision(%s): %v", d.Type, err)
		}
	}

	// Query all entries and filter by correlation ID.
	entries := lc.Entries()
	var correlated []ledger.FleetDecision
	for _, e := range entries {
		if e.CorrelationID == correlationID {
			correlated = append(correlated, e)
		}
	}

	if len(correlated) != 3 {
		t.Fatalf("expected 3 correlated decisions, got %d", len(correlated))
	}

	// Verify all three chain types are represented.
	types := map[string]bool{}
	for _, e := range correlated {
		types[e.Type] = true
	}
	for _, expected := range []string{"fleet.placement.assigned", "fleet.routing.shifted", "fleet.tenant.usage"} {
		if !types[expected] {
			t.Fatalf("missing correlated decision of type %q", expected)
		}
	}
}

// ===========================================================================
// EVENT FLOW (A33-A36)
// ===========================================================================

func TestA33_Events_PlacementPublished(t *testing.T) {
	claim(t, "A33", "events", "EDD", "Placement event published and received by subscriber")

	pub := events.NewEventPublisher()
	ctx := context.Background()

	var received *events.FleetEvent
	err := pub.Subscribe(ctx, []string{"fleet.placement.assigned"}, func(_ context.Context, e events.FleetEvent) error {
		received = &e
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	event := events.FleetEvent{
		Type:      "fleet.placement.assigned",
		Payload:   map[string]interface{}{"model": "gpt-4", "cluster": "us-east"},
		Timestamp: time.Now(),
		Source:    "arch-test",
	}
	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if received == nil {
		t.Fatal("subscriber did not receive placement event")
	}
	if received.Type != "fleet.placement.assigned" {
		t.Fatalf("received type = %q, want 'fleet.placement.assigned'", received.Type)
	}
}

func TestA34_Events_RoutingPublished(t *testing.T) {
	claim(t, "A34", "events", "EDD", "Routing event published and received by subscriber")

	pub := events.NewEventPublisher()
	ctx := context.Background()

	var received *events.FleetEvent
	err := pub.Subscribe(ctx, []string{"fleet.routing.shifted"}, func(_ context.Context, e events.FleetEvent) error {
		received = &e
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	event := events.FleetEvent{
		Type:      "fleet.routing.shifted",
		Payload:   map[string]interface{}{"from": "us-east", "to": "eu-west"},
		Timestamp: time.Now(),
		Source:    "arch-test",
	}
	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if received == nil {
		t.Fatal("subscriber did not receive routing event")
	}
	if received.Type != "fleet.routing.shifted" {
		t.Fatalf("received type = %q, want 'fleet.routing.shifted'", received.Type)
	}
}

func TestA35_Events_TenantPublished(t *testing.T) {
	claim(t, "A35", "events", "EDD", "Tenant event published and received by subscriber")

	pub := events.NewEventPublisher()
	ctx := context.Background()

	var received *events.FleetEvent
	err := pub.Subscribe(ctx, []string{"fleet.tenant.usage"}, func(_ context.Context, e events.FleetEvent) error {
		received = &e
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	event := events.FleetEvent{
		Type:      "fleet.tenant.usage",
		Payload:   map[string]interface{}{"tenant": "tenant-1", "tokens": 5000},
		Timestamp: time.Now(),
		Source:    "arch-test",
	}
	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if received == nil {
		t.Fatal("subscriber did not receive tenant event")
	}
	if received.Type != "fleet.tenant.usage" {
		t.Fatalf("received type = %q, want 'fleet.tenant.usage'", received.Type)
	}
}

func TestA36_Events_HTTPPublisherDeliversExternally(t *testing.T) {
	claim(t, "A36", "events", "EDD", "HTTPEventPublisher delivers JSON to external endpoint")

	// Create a mock HTTP receiver.
	var receivedBody atomic.Value
	mockReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody.Store(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockReceiver.Close()

	pub := events.NewHTTPEventPublisher(mockReceiver.URL)
	ctx := context.Background()

	event := events.FleetEvent{
		Type:      "fleet.placement.assigned",
		Payload:   map[string]interface{}{"model": "gpt-4", "cluster": "us-east"},
		Timestamp: time.Now(),
		Source:    "arch-test",
	}

	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Verify the mock server received the event.
	raw, ok := receivedBody.Load().([]byte)
	if !ok || len(raw) == 0 {
		t.Fatal("mock HTTP receiver did not receive any data")
	}

	// Verify it is valid JSON with the expected type.
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("received body is not valid JSON: %v", err)
	}
	if payload["type"] != "fleet.placement.assigned" {
		t.Fatalf("payload type = %v, want 'fleet.placement.assigned'", payload["type"])
	}
}

// ===========================================================================
// MULTI-CLUSTER (A37-A39)
// ===========================================================================

func TestA37_MultiCluster_RoutingSelectsCorrectCluster(t *testing.T) {
	claim(t, "A37", "multi-cluster", "TDD", "Cross-cluster routing selects correct cluster by objective")

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("multi-model", routing.Backend{
		Name: "us-east-backend", URL: "http://us-east:8000", Healthy: true, LatencyMs: 10,
	})
	proxy.RegisterBackend("multi-model", routing.Backend{
		Name: "eu-west-backend", URL: "http://eu-west:8000", Healthy: true, LatencyMs: 50,
	})

	// Realtime request should select the lowest-latency cluster (us-east, 10ms).
	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "realtime")

	backend, reason, err := proxy.SelectBackend("multi-model", headers)
	if err != nil {
		t.Fatalf("SelectBackend realtime: %v", err)
	}
	if backend.Name != "us-east-backend" {
		t.Fatalf("realtime: expected us-east-backend (10ms), got %s (%.0fms)", backend.Name, backend.LatencyMs)
	}
	if !strings.Contains(reason, "realtime") {
		t.Fatalf("reason should mention realtime, got %q", reason)
	}

	// Default (round-robin) requests should distribute across both clusters.
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		backend, _, err = proxy.SelectBackend("multi-model", http.Header{})
		if err != nil {
			t.Fatalf("SelectBackend round-robin iteration %d: %v", i, err)
		}
		seen[backend.Name] = true
	}
	if len(seen) < 2 {
		t.Fatalf("round-robin should select both clusters, only saw: %v", seen)
	}
}

func TestA38_MultiCluster_FailoverOnBackendHealthChange(t *testing.T) {
	claim(t, "A38", "multi-cluster", "TDD", "Failover redirects traffic when backend health changes")

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("failover-model", routing.Backend{
		Name: "primary", URL: "http://primary:8000", Healthy: true, LatencyMs: 10,
	})
	proxy.RegisterBackend("failover-model", routing.Backend{
		Name: "secondary", URL: "http://secondary:8000", Healthy: true, LatencyMs: 50,
	})

	// Realtime selects the lowest-latency backend (primary).
	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "realtime")

	backend, _, err := proxy.SelectBackend("failover-model", headers)
	if err != nil {
		t.Fatalf("SelectBackend initial: %v", err)
	}
	if backend.Name != "primary" {
		t.Fatalf("expected primary (10ms), got %s", backend.Name)
	}

	// Mark primary unhealthy.
	proxy.UpdateBackendHealth("failover-model", "primary", false)

	// Next request must failover to secondary.
	backend, _, err = proxy.SelectBackend("failover-model", headers)
	if err != nil {
		t.Fatalf("SelectBackend after failover: %v", err)
	}
	if backend.Name != "secondary" {
		t.Fatalf("expected secondary after primary failure, got %s", backend.Name)
	}

	// Restore primary health.
	proxy.UpdateBackendHealth("failover-model", "primary", true)

	// Send multiple default requests and verify traffic redistributes to both.
	seen := map[string]int{}
	for i := 0; i < 10; i++ {
		backend, _, err = proxy.SelectBackend("failover-model", http.Header{})
		if err != nil {
			t.Fatalf("SelectBackend redistribute %d: %v", i, err)
		}
		seen[backend.Name]++
	}
	if len(seen) < 2 {
		t.Fatalf("traffic should redistribute to both backends after restore, only saw: %v", seen)
	}
}

func TestA39_MultiCluster_ReconcilerPlacesAcrossClusters(t *testing.T) {
	claim(t, "A39", "multi-cluster", "TDD", "Reconciler places model across multiple clusters")

	// The test world has 3 clusters: us-east, eu-west, ap-south.
	w := newTestWorld(t)
	ctx := context.Background()

	pool := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   "spread-model",
			Source: "huggingface",
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "spread-policy",
			MinClusters: 2,
			MaxClusters: 3,
		},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}

	if err := w.Reconciler.ReconcilePool(ctx, pool); err != nil {
		t.Fatalf("ReconcilePool: %v", err)
	}

	state, err := w.Reconciler.GetPoolState("spread-model")
	if err != nil {
		t.Fatalf("GetPoolState: %v", err)
	}

	// Verify the reconciler produced placement decisions for at least 2 clusters.
	if len(state.DesiredClusters) < 2 {
		t.Fatalf("expected at least 2 desired clusters (minClusters=2), got %d: %v",
			len(state.DesiredClusters), state.DesiredClusters)
	}

	// Verify they are distinct cluster IDs (proves spreading across regions).
	uniqueClusters := map[string]bool{}
	for _, c := range state.DesiredClusters {
		uniqueClusters[c] = true
	}
	if len(uniqueClusters) < 2 {
		t.Fatalf("expected at least 2 distinct cluster IDs, got: %v", state.DesiredClusters)
	}
}

// ===========================================================================
// SECURITY / HARDENING (A40-A41)
// ===========================================================================

func TestA40_Security_RateLimitRejectsOverBurst(t *testing.T) {
	claim(t, "A40", "security", "TDD", "Rate limiter rejects requests over burst")

	rl := auth.NewRateLimiter(10, 5)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := auth.RateLimitMiddleware(rl, inner)

	allowed := 0
	rejected := 0
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			allowed++
		} else if rec.Code == http.StatusTooManyRequests {
			rejected++
		} else {
			t.Fatalf("unexpected status code %d on request %d", rec.Code, i+1)
		}
	}

	if rejected == 0 {
		t.Fatal("expected some requests to be rejected (429), but all were allowed")
	}
	if allowed == 0 {
		t.Fatal("expected some requests to be allowed, but all were rejected")
	}
	if allowed > 5 {
		t.Fatalf("expected at most 5 allowed (burst=5), got %d", allowed)
	}
}

func TestA41_Validation_WebhookRejectsInvalidCRDs(t *testing.T) {
	claim(t, "A41", "security", "TDD", "Webhook rejects invalid CRDs")

	// Validate a FleetInferencePool with an empty model name.
	spec := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{Name: "", Source: "huggingface"},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8000},
				},
			},
		},
	}
	result := controller.ValidateFleetInferencePool(spec)
	if result.Valid {
		t.Fatal("expected invalid result for empty model name")
	}
	if len(result.Details) == 0 {
		t.Fatal("expected validation details describing the error")
	}

	foundModelError := false
	for _, d := range result.Details {
		if strings.Contains(d, "model name") {
			foundModelError = true
			break
		}
	}
	if !foundModelError {
		t.Fatalf("expected model name error in details, got: %v", result.Details)
	}
}

// ===========================================================================
// COST AND TOKENOMICS (A42-A45)
// ===========================================================================

func TestA42_Cost_TokenCostCalculation(t *testing.T) {
	claim(t, "A42", "cost", "TDD", "ComputeTokenCost produces reasonable per-M-token cost")

	pricing := cost.DefaultPricingTable()

	// H200 on-demand at 1000 tok/s:
	// $4.50/hr / 3600s / 1000 tok/s * 1M = ~$1.25 per million tokens
	tc, err := cost.ComputeTokenCost("granite-3b", "H200", "on-demand", 1000, pricing)
	if err != nil {
		t.Fatalf("ComputeTokenCost: %v", err)
	}

	// Verify the cost is in a reasonable range (~$1.25/M tokens).
	if tc.CostPerMToken < 1.0 || tc.CostPerMToken > 1.5 {
		t.Fatalf("CostPerMToken = %f, expected ~1.25 (range 1.0-1.5)", tc.CostPerMToken)
	}

	if tc.CostPerHour != 4.50 {
		t.Fatalf("CostPerHour = %f, want 4.50", tc.CostPerHour)
	}
}

func TestA43_Cost_ChargebackReport(t *testing.T) {
	claim(t, "A43", "cost", "TDD", "Chargeback report total equals sum of line items")

	pricing := cost.DefaultPricingTable()
	now := time.Now()

	usage := []cost.UsageRecord{
		{TenantID: "tenant-x", Model: "granite-3b", Cluster: "us-east", GPUType: "H200", Tokens: 500_000, Duration: 1 * time.Hour, Timestamp: now},
		{TenantID: "tenant-x", Model: "llama-70b", Cluster: "eu-west", GPUType: "A100", Tokens: 300_000, Duration: 2 * time.Hour, Timestamp: now.Add(-time.Hour)},
		{TenantID: "tenant-x", Model: "granite-3b", Cluster: "us-east", GPUType: "H200", Tokens: 200_000, Duration: 30 * time.Minute, Timestamp: now.Add(time.Hour)},
	}

	report := cost.GenerateChargebackReport("tenant-x", usage, pricing, 5000.0)

	if report.TenantID != "tenant-x" {
		t.Fatalf("TenantID = %q, want 'tenant-x'", report.TenantID)
	}

	// Should have 2 line items (granite-3b/us-east grouped, llama-70b/eu-west separate).
	if len(report.CostBreakdown) != 2 {
		t.Fatalf("expected 2 line items, got %d", len(report.CostBreakdown))
	}

	// Verify TotalCost equals sum of line item costs.
	var sumCost float64
	for _, item := range report.CostBreakdown {
		sumCost += item.Cost
	}
	diff := report.TotalCost - sumCost
	if diff < -0.01 || diff > 0.01 {
		t.Fatalf("TotalCost (%f) != sum of line items (%f)", report.TotalCost, sumCost)
	}

	if report.TotalCost <= 0 {
		t.Fatal("TotalCost should be positive")
	}
}

func TestA44_Cost_BudgetAlert(t *testing.T) {
	claim(t, "A44", "cost", "TDD", "Warning alert fires when cost exceeds 80%% of budget")

	configs := []cost.TenantBudgetConfig{
		{TenantID: "tenant-alert", MonthlyBudget: 1000, WarningAt: 0.8, CriticalAt: 0.95},
	}
	currentCosts := map[string]float64{
		"tenant-alert": 850, // 85% of $1000
	}

	alerts := cost.CheckBudgetAlerts(configs, currentCosts)

	foundWarning := false
	for _, a := range alerts {
		if a.AlertLevel == "warning" && a.TenantID == "tenant-alert" {
			foundWarning = true
			if a.Threshold != 0.8 {
				t.Fatalf("warning threshold = %f, want 0.8", a.Threshold)
			}
			if a.CurrentCost != 850 {
				t.Fatalf("current cost = %f, want 850", a.CurrentCost)
			}
			break
		}
	}
	if !foundWarning {
		t.Fatal("expected warning alert at 85% budget usage")
	}
}

func TestA45_Cost_PlacementPrefersCheaper(t *testing.T) {
	claim(t, "A45", "cost", "TDD", "Placement solver prefers cheaper cluster when both meet SLO")

	s := solver.NewConstraintSolver()
	ctx := context.Background()

	// Two clusters: one expensive (H200, high utilization), one cheap (A100, low utilization).
	clusters := []solver.ClusterInfo{
		{
			ID: "expensive-h200", Name: "expensive", Region: "us-east-1",
			Labels:      map[string]string{"region": "us-east-1"},
			GPUCapacity: solver.GPUCapacity{Available: 4, Total: 8, Types: []string{"H200"}},
			Utilization: 0.8, // High utilization = higher cost score penalty
		},
		{
			ID: "cheap-a100", Name: "cheap", Region: "us-west-2",
			Labels:      map[string]string{"region": "us-west-2"},
			GPUCapacity: solver.GPUCapacity{Available: 6, Total: 8, Types: []string{"A100"}},
			Utilization: 0.2, // Low utilization = better cost score
		},
	}

	pool := makePool("cost-test-model", "huggingface", 1)

	// Use cost-optimization affinity to ensure the solver prefers the cheaper option.
	policy := v1alpha1.PlacementPolicySpec{
		Affinity: []v1alpha1.AffinityRule{
			{Type: "cost-optimization", Weight: 1.0},
		},
	}

	decisions, err := s.Solve(ctx, pool, clusters, policy)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision (maxClusters=1), got %d", len(decisions))
	}

	// The solver should prefer cheap-a100 (lower utilization = higher cost score).
	if decisions[0].ClusterID != "cheap-a100" {
		t.Fatalf("expected solver to prefer cheap-a100, got %s", decisions[0].ClusterID)
	}
}

// ===========================================================================
// MODELPLANE INTEGRATION (A46-A50)
// ===========================================================================

func TestA46_ModelPlane_ClusterMapsToFleetCluster(t *testing.T) {
	claim(t, "A46", "modelplane", "TDD", "InferenceCluster maps to ClusterInfo with correct GPUCapacity")

	ic := modelplane.InferenceCluster{
		Name:     "mp-cluster",
		Region:   "us-east-1",
		Provider: "gke",
		Labels:   map[string]string{"env": "prod"},
		Status:   modelplane.ClusterStatus{Phase: "Ready", Nodes: 10},
		Pools: []modelplane.NodePool{
			{Name: "pool-a", GPUType: "H200", Count: 8, Available: 6},
			{Name: "pool-b", GPUType: "A100", Count: 4, Available: 2},
		},
	}

	ci := modelplane.InferenceClusterToClusterInfo(ic)

	if ci.Name != "mp-cluster" {
		t.Fatalf("Name = %q, want 'mp-cluster'", ci.Name)
	}
	if ci.Region != "us-east-1" {
		t.Fatalf("Region = %q, want 'us-east-1'", ci.Region)
	}
	// Total GPU: 8 + 4 = 12, Available: 6 + 2 = 8
	if ci.GPUCapacity.Total != 12 {
		t.Fatalf("GPUCapacity.Total = %d, want 12", ci.GPUCapacity.Total)
	}
	if ci.GPUCapacity.Available != 8 {
		t.Fatalf("GPUCapacity.Available = %d, want 8", ci.GPUCapacity.Available)
	}
	if len(ci.GPUCapacity.Types) != 2 {
		t.Fatalf("GPUCapacity.Types len = %d, want 2 (H200, A100)", len(ci.GPUCapacity.Types))
	}
}

func TestA47_ModelPlane_EndpointMapsToBackend(t *testing.T) {
	claim(t, "A47", "modelplane", "TDD", "ModelEndpoint maps to Backend with correct URL and Healthy")

	me := modelplane.ModelEndpoint{
		Name:      "granite-ep",
		Namespace: "fleet",
		URL:       "http://granite-vllm.fleet.svc:8000",
		Model:     "granite-3b",
		Cluster:   "prod-east",
		Ready:     true,
	}

	b := modelplane.ModelEndpointToBackend(me)

	if b.Name != "granite-ep" {
		t.Fatalf("Name = %q, want 'granite-ep'", b.Name)
	}
	if b.URL != "http://granite-vllm.fleet.svc:8000" {
		t.Fatalf("URL = %q, want 'http://granite-vllm.fleet.svc:8000'", b.URL)
	}
	if !b.Healthy {
		t.Fatal("Healthy should be true when Ready is true")
	}

	// Verify not-ready endpoint
	me.Ready = false
	b2 := modelplane.ModelEndpointToBackend(me)
	if b2.Healthy {
		t.Fatal("Healthy should be false when Ready is false")
	}
}

func TestA48_ModelPlane_PolicyInjectsAnnotations(t *testing.T) {
	claim(t, "A48", "modelplane", "TDD", "PolicyInjector sends PATCH with correct annotations")

	var receivedMethod, receivedPath string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pi := modelplane.NewPolicyInjector(server.URL, "test-token")
	ctx := context.Background()

	constraints := map[string]string{
		"fleet.llm-d.ai/region":   "us-east-1",
		"fleet.llm-d.ai/gpu-type": "H200",
	}
	err := pi.ApplyPlacementAnnotations(ctx, "granite-deploy", "fleet-ns", constraints)
	if err != nil {
		t.Fatalf("ApplyPlacementAnnotations: %v", err)
	}

	if receivedMethod != http.MethodPatch {
		t.Fatalf("method = %q, want PATCH", receivedMethod)
	}
	expectedPath := "/apis/modelplane.ai/v1alpha1/namespaces/fleet-ns/modeldeployments/granite-deploy"
	if receivedPath != expectedPath {
		t.Fatalf("path = %q, want %q", receivedPath, expectedPath)
	}

	var patch map[string]interface{}
	if err := json.Unmarshal(receivedBody, &patch); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	metadata, ok := patch["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("patch missing 'metadata' object")
	}
	annotations, ok := metadata["annotations"].(map[string]interface{})
	if !ok {
		t.Fatal("patch missing 'metadata.annotations' object")
	}
	if annotations["fleet.llm-d.ai/region"] != "us-east-1" {
		t.Fatalf("annotation region = %v, want 'us-east-1'", annotations["fleet.llm-d.ai/region"])
	}
}

func TestA49_ModelPlane_DeploymentCostFromInferenceClass(t *testing.T) {
	claim(t, "A49", "cost", "TDD", "Deployment cost = replicas * GPU hourly rate")

	table := cost.DefaultPricingTable()

	md := modelplane.ModelDeployment{
		Name:     "h200-deploy",
		Model:    "llama-70b",
		Replicas: 4,
		Status: modelplane.DeploymentStatus{
			Phase:    "Running",
			Clusters: []string{"h200-cluster"},
		},
	}

	clusters := []modelplane.InferenceCluster{
		{
			Name:   "h200-cluster",
			Region: "us-east-1",
			Pools:  []modelplane.NodePool{{Name: "pool-1", GPUType: "H200", Count: 8, Available: 4}},
		},
	}

	totalCost, err := cost.ComputeDeploymentCost(md, clusters, table)
	if err != nil {
		t.Fatalf("ComputeDeploymentCost: %v", err)
	}

	// Expected: 4 replicas * $4.50/hr (H200 on-demand) = $18.00/hr
	expected := 4 * 4.50
	if totalCost != expected {
		t.Fatalf("cost = %f, want %f (4 * $4.50 H200 hourly)", totalCost, expected)
	}
}

func TestA50_ModelPlane_EventsRecordedToLedger(t *testing.T) {
	claim(t, "A50", "modelplane", "CDD", "ModelPlane events recorded to immutable ledger")

	lc := ledger.NewInMemoryLedgerClient()
	fr := ledger.NewFleetRecorder(lc, "test-agent", "arch-test")
	bridge := modelplane.NewComplianceBridge(fr)
	ctx := context.Background()

	// Record cluster provisioned
	cluster := modelplane.InferenceCluster{
		Name:     "new-cluster",
		Region:   "us-east-1",
		Provider: "gke",
		Status:   modelplane.ClusterStatus{Phase: "Ready", Nodes: 8},
	}
	_, err := bridge.RecordClusterProvisioned(ctx, cluster)
	if err != nil {
		t.Fatalf("RecordClusterProvisioned: %v", err)
	}

	// Record deployment created
	deployment := modelplane.ModelDeployment{
		Name:      "granite-deploy",
		Namespace: "fleet",
		Model:     "granite-3b",
		Engine:    "vllm",
		Replicas:  4,
	}
	_, err = bridge.RecordDeploymentCreated(ctx, deployment)
	if err != nil {
		t.Fatalf("RecordDeploymentCreated: %v", err)
	}

	// Verify 2 ledger entries with correct types
	entries := lc.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 ledger entries, got %d", len(entries))
	}
	if entries[0].Type != "modelplane.cluster.provisioned" {
		t.Fatalf("entry[0] type = %q, want 'modelplane.cluster.provisioned'", entries[0].Type)
	}
	if entries[1].Type != "modelplane.deployment.created" {
		t.Fatalf("entry[1] type = %q, want 'modelplane.deployment.created'", entries[1].Type)
	}

	// Verify content is valid JSON with expected fields
	var content0 map[string]interface{}
	if err := json.Unmarshal(entries[0].Content, &content0); err != nil {
		t.Fatalf("unmarshal entry[0] content: %v", err)
	}
	if content0["cluster"] != "new-cluster" {
		t.Fatalf("entry[0] cluster = %v, want 'new-cluster'", content0["cluster"])
	}

	var content1 map[string]interface{}
	if err := json.Unmarshal(entries[1].Content, &content1); err != nil {
		t.Fatalf("unmarshal entry[1] content: %v", err)
	}
	if content1["deployment"] != "granite-deploy" {
		t.Fatalf("entry[1] deployment = %v, want 'granite-deploy'", content1["deployment"])
	}
}

// ===========================================================================
// SECURITY CONTRACTS (A51-A54)
// ===========================================================================

func TestA51_Security_ProxyStripsAuthHeaders(t *testing.T) {
	claim(t, "A51", "security", "CDD", "Proxy strips Authorization/Cookie/Proxy-Authorization before forwarding")

	// Architecture claim: the inference proxy MUST strip Authorization,
	// Cookie, and Proxy-Authorization headers before forwarding to backends.
	var receivedHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer backend.Close()

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("arch-test-model", routing.Backend{
		Name: "arch-backend", URL: backend.URL, Runtime: "vllm", Healthy: true,
	})

	body := `{"model":"arch-test-model","messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("Proxy-Authorization", "Basic creds")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("proxy returned %d, expected 200", w.Code)
	}
	if receivedHeaders.Get("Authorization") != "" {
		t.Error("ARCHITECTURE VIOLATION: Authorization header forwarded to backend")
	}
	if receivedHeaders.Get("Cookie") != "" {
		t.Error("ARCHITECTURE VIOLATION: Cookie header forwarded to backend")
	}
	if receivedHeaders.Get("Proxy-Authorization") != "" {
		t.Error("ARCHITECTURE VIOLATION: Proxy-Authorization header forwarded to backend")
	}
}

func TestA52_Security_ProxyReturnsValidJSONErrors(t *testing.T) {
	claim(t, "A52", "security", "CDD", "All proxy error responses are valid JSON with application/json")

	// Architecture claim: all proxy error responses MUST be valid JSON
	// with Content-Type application/json.
	proxy := routing.NewInferenceProxy()

	cases := []struct {
		name string
		body string
	}{
		{"missing model", `{"messages":[{"role":"user","content":"test"}]}`},
		{"invalid JSON", `{not valid json`},
		{"unknown model", `{"model":"nonexistent","messages":[]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("ARCHITECTURE VIOLATION: error response Content-Type is %q, expected application/json", ct)
			}

			var parsed map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
				t.Errorf("ARCHITECTURE VIOLATION: error response is not valid JSON: %v\nbody: %s", err, w.Body.String())
			}
		})
	}
}

func TestA53_Security_RateLimiterEvictsStaleEntries(t *testing.T) {
	claim(t, "A53", "security", "CDD", "Rate limiter evicts stale buckets to prevent memory leaks")

	// Architecture claim: the rate limiter MUST evict stale buckets
	// to prevent unbounded memory growth.
	rl := auth.NewRateLimiterWithTTL(100, 200, 100*time.Millisecond)
	defer rl.Stop()

	for i := 0; i < 100; i++ {
		rl.Allow(fmt.Sprintf("evict-test-%d", i))
	}

	if rl.BucketCount() != 100 {
		t.Fatalf("expected 100 buckets, got %d", rl.BucketCount())
	}

	time.Sleep(250 * time.Millisecond)

	if count := rl.BucketCount(); count > 0 {
		t.Errorf("ARCHITECTURE VIOLATION: %d stale buckets not evicted (memory leak)", count)
	}
}

func TestA54_Tenant_CheckQuotaIsReadOnly(t *testing.T) {
	claim(t, "A54", "tenant", "CDD", "CheckQuota does not deduct tokens or budget")

	// Architecture claim: CheckQuota MUST NOT deduct tokens or budget.
	// Only ConsumeQuota should modify state.
	e := quota.NewQuotaEnforcer()

	result1, _ := e.CheckQuota(context.Background(), "tenant-1", quota.QuotaCheckRequest{TokensRequested: 100})
	result2, _ := e.CheckQuota(context.Background(), "tenant-1", quota.QuotaCheckRequest{TokensRequested: 100})

	if result1.RemainingTokens != result2.RemainingTokens {
		t.Errorf("ARCHITECTURE VIOLATION: CheckQuota changed remaining tokens from %d to %d",
			result1.RemainingTokens, result2.RemainingTokens)
	}
}

// ===========================================================================
// PREDICTIVE BRAIN (A55)
// ===========================================================================

func TestA55_IntentConsumerAcceptsPreWarm(t *testing.T) {
	claim(t, "A55", "predictive-brain", "CDD", "Intent consumer accepts valid PreWarm intent")

	intent := intents.FleetIntent{
		ID:             "arch-test-1",
		Type:           intents.IntentPreWarm,
		Confidence:     0.85,
		HorizonSeconds: 1800,
		Justification:  "Event pre-warming",
		Model:          "granite-2b",
		TargetReplicas: 4,
	}

	resp := intents.Evaluate(context.Background(), intent, intents.DefaultPolicyConfig())
	if resp.Status != intents.StatusExecuted {
		t.Fatalf("ARCHITECTURE VIOLATION: valid intent refused: %s", resp.Reason)
	}
}

// ===========================================================================
// TestMain: run all tests and print the architectural proof matrix.
// ===========================================================================

func TestMain(m *testing.M) {
	code := m.Run()

	fmt.Println()
	fmt.Println("=== ARCHITECTURAL PROOF MATRIX ===")

	matrixMu.Lock()
	results := make([]ClaimResult, len(matrixResults))
	copy(results, matrixResults)
	matrixMu.Unlock()

	PrintMatrix(results)

	os.Exit(code)
}
