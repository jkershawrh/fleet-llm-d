package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"expvar"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
	"github.com/llm-d/fleet-llm-d/pkg/cost"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/kvcache/transfer"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/lifecycle/rollout"
	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
	"github.com/llm-d/fleet-llm-d/pkg/observability/metrics"
	"github.com/llm-d/fleet-llm-d/pkg/placement/scorer"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/routing/balancer"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/metering"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/quota"
)

// Prometheus-style counters tracked via expvar.
var (
	requestsTotal = expvar.NewInt("fleet_requests_total")
	errorsTotal   = expvar.NewInt("fleet_errors_total")
	clustersGauge = expvar.NewInt("fleet_clusters_registered")
	poolsGauge    = expvar.NewInt("fleet_pools_active")
	tenantsGauge  = expvar.NewInt("fleet_tenants_active")
	rolloutsGauge = expvar.NewInt("fleet_rollouts_active")
)

// FleetController is the top-level controller that coordinates all fleet
// management capabilities including placement, routing, autoscaling,
// tenant management, lifecycle, observability, KV cache, and cluster operations.
type FleetController struct {
	// Capability components
	Solver               solver.ConstraintSolver
	Scorer               *scorer.CompositeScorer
	RoutingEvaluator     policy.RoutingPolicyEvaluator
	LoadBalancer         balancer.LoadBalancer
	MetricsCollector     collector.MetricsCollector
	Optimizer            optimizer.FleetOptimizer
	QuotaEnforcer        quota.QuotaEnforcer
	UsageTracker         metering.UsageTracker
	RolloutController    rollout.RolloutController
	MetricsFederator     metrics.MetricsFederator
	TransferOrchestrator transfer.TransferOrchestrator
	ClusterClient        client.MultiClusterClient
	EventPublisher       events.EventPublisher

	// Reconciler watches fleet CRDs and reconciles desired state
	Reconciler *controller.Reconciler

	// CRDWatcher polls the K8s API for CRD changes (optional, only when kube-api is configured)
	CRDWatcher *controller.CRDWatcher

	// Ledger integration
	FleetRecorder *ledger.FleetRecorder

	// Inference proxy
	InferenceProxy *routing.InferenceProxy

	// Cost and pricing
	PricingTable *cost.PricingTable

	// Repositories for CRUD operations
	ClusterRepo postgres.ClusterRepository
	PoolRepo    postgres.FleetPoolRepository
	TenantRepo  postgres.TenantRepository
	RolloutRepo postgres.RolloutRepository

	// ModelPlane integration
	ModelPlaneWatcher    *modelplane.ModelPlaneWatcher
	ModelPlaneBridge     *modelplane.ComplianceBridge

	// Server state
	ready atomic.Bool
}

// NewFleetController creates a new FleetController with all components
// initialized using their default constructors. The backendVLLM and backendOVMS
// parameters specify the base URLs for the default inference backends. The
// kubeAPI and namespace parameters are optional; when kubeAPI is non-empty a
// CRDWatcher is created that polls the Kubernetes API for FleetInferencePool
// changes.
func NewFleetController(ledgerEndpoint, backendVLLM, backendOVMS, kubeAPI, namespace string) *FleetController {
	return NewFleetControllerWithLedgerConfig(ledger.Config{Mode: ledger.ModeMemory, Endpoint: ledgerEndpoint}, backendVLLM, backendOVMS, kubeAPI, namespace)
}

// NewFleetControllerWithLedgerConfig creates a FleetController with an
// explicit ledger backend configuration.
func NewFleetControllerWithLedgerConfig(ledgerCfg ledger.Config, backendVLLM, backendOVMS, kubeAPI, namespace string) *FleetController {
	ledgerClient, err := ledger.NewLedgerClientWithConfig(ledgerCfg)
	if err != nil {
		log.Printf("invalid ledger config (%s): %v; falling back to memory ledger", ledgerCfg.Mode, err)
		ledgerClient = ledger.NewInMemoryLedgerClient()
	}

	proxy := routing.NewInferenceProxy()
	proxy.RegisterBackend("granite-3.3-2b", routing.Backend{
		Name:      "vllm-cpu",
		URL:       backendVLLM,
		Runtime:   "vllm",
		Healthy:   true,
		LatencyMs: 500,
	})
	ovmsBackend := routing.Backend{
		Name:       "ovms-granite",
		URL:        backendOVMS,
		Runtime:    "ovms",
		PathPrefix: "/v3",
		Healthy:    true,
		LatencyMs:  200,
	}
	proxy.RegisterBackend("granite-sovereign", ovmsBackend)
	proxy.RegisterBackend("granite-3.2-sovereign", ovmsBackend)

	clusterRepo := postgres.NewInMemoryClusterRepository()
	clusterClient := client.NewRepositoryClusterClient(clusterRepo)
	fleetRecorder := ledger.NewFleetRecorder(ledgerClient, "fleet-controller", "fleet-llm-d")
	constraintSolver := solver.NewConstraintSolver()

	// Create reconciler wired to the cluster client and constraint solver.
	reconciler := controller.NewReconciler(constraintSolver, clusterClient.ListClusters)

	// Wire the onChange callback so every placement decision is recorded
	// to the ARE immutable ledger.
	reconciler.SetOnChange(func(pool *controller.FleetPoolState) {
		for _, clusterID := range pool.DesiredClusters {
			if _, err := fleetRecorder.RecordPlacement(
				context.Background(),
				pool.Model, clusterID, 1, "", "reconciler placement",
			); err != nil {
				log.Printf("failed to record placement for %s -> %s: %v", pool.Model, clusterID, err)
			}
		}
	})

	// Create CRDWatcher if Kubernetes API is configured.
	var crdWatcher *controller.CRDWatcher
	if kubeAPI != "" {
		if namespace == "" {
			namespace = "default"
		}
		// Read service account token for in-cluster auth.
		token := ""
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			token = string(data)
		}
		crdWatcher = controller.NewCRDWatcher(kubeAPI, namespace, token, reconciler)
	}

	return &FleetController{
		Solver: constraintSolver,
		Scorer: scorer.NewCompositeScorer([]scorer.WeightedScorer{
			{Scorer: scorer.NewCostScorer(), Weight: 0.3},
			{Scorer: scorer.NewCapacityScorer(), Weight: 0.3},
			{Scorer: scorer.NewLocalityScorer(), Weight: 0.2},
			{Scorer: scorer.NewKVCacheAffinityScorer(), Weight: 0.2},
		}),
		RoutingEvaluator:     policy.NewRoutingPolicyEvaluator(),
		LoadBalancer:         balancer.NewWeightedBalancer(),
		MetricsCollector:     collector.NewMetricsCollector(),
		Optimizer:            optimizer.NewFleetOptimizer(),
		QuotaEnforcer:        quota.NewQuotaEnforcer(),
		UsageTracker:         metering.NewUsageTracker(),
		RolloutController:    rollout.NewRolloutController(),
		MetricsFederator:     metrics.NewMetricsFederator(),
		TransferOrchestrator: transfer.NewTransferOrchestrator(),
		ClusterClient:        clusterClient,
		EventPublisher:       events.NewEventPublisher(),
		PricingTable:         cost.DefaultPricingTable(),
		Reconciler:           reconciler,
		CRDWatcher:           crdWatcher,
		FleetRecorder:        fleetRecorder,
		InferenceProxy:       proxy,
		ClusterRepo:          clusterRepo,
		PoolRepo:             postgres.NewInMemoryFleetPoolRepository(),
		TenantRepo:           postgres.NewInMemoryTenantRepository(),
		RolloutRepo:          postgres.NewInMemoryRolloutRepository(),
	}
}

// ----------------------------------------------------------------------------
// HTTP API Handlers
// ----------------------------------------------------------------------------

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("error encoding JSON response: %v", err)
	}
}

// writeError writes an error JSON response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleHealthz is the liveness probe.
func (fc *FleetController) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz is the readiness probe.
func (fc *FleetController) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !fc.ready.Load() {
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleListClusters returns all registered clusters.
func (fc *FleetController) handleListClusters(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	clusters, err := fc.ClusterClient.ListClusters(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, clusters)
}

// clusterRegistrationRequest is the JSON body for POST /api/v1/clusters.
type clusterRegistrationRequest struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels"`
}

// handleRegisterCluster registers a new cluster.
func (fc *FleetController) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var req clusterRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	reg := client.ClusterRegistration{
		ID:     req.ID,
		Name:   req.Name,
		Region: req.Region,
		Labels: req.Labels,
	}
	reg, err := client.NormalizeClusterRegistration(reg)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := fc.ClusterClient.RegisterCluster(r.Context(), reg); err != nil {
		errorsTotal.Add(1)
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "conflict") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	clustersGauge.Add(1)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered", "id": reg.ID})
}

// handleDeregisterCluster removes a cluster by ID.
func (fc *FleetController) handleDeregisterCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if err := fc.ClusterClient.DeregisterCluster(r.Context(), id); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	clustersGauge.Add(-1)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deregistered", "id": id})
}

// handleListPools returns all fleet inference pools. It merges data from
// the reconciler (which tracks live CRD state) with the repository.
func (fc *FleetController) handleListPools(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	// Prefer reconciler state when available -- it reflects live CRD watches.
	if fc.Reconciler != nil {
		reconciled := fc.Reconciler.ListPools()
		if len(reconciled) > 0 {
			writeJSON(w, http.StatusOK, reconciled)
			return
		}
	}

	// Fall back to the store-backed repository.
	pools, err := fc.PoolRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pools)
}

// handleGetPoolState returns the reconciled state for a single pool by name.
func (fc *FleetController) handleGetPoolState(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "pool name is required")
		return
	}
	state, err := fc.Reconciler.GetPoolState(name)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleListTenants returns all tenants.
func (fc *FleetController) handleListTenants(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	tenants, err := fc.TenantRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// handleTenantUsage returns usage for a specific tenant.
func (fc *FleetController) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "tenant id is required")
		return
	}
	// Default to current month.
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := now

	period := metering.TimePeriod{Start: start, End: end}
	usage, err := fc.UsageTracker.GetUsage(r.Context(), id, period)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

// handleFleetMetrics returns fleet-wide aggregated metrics.
func (fc *FleetController) handleFleetMetrics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	clusters, err := fc.ClusterClient.ListClusters(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clusterIDs := make([]string, len(clusters))
	for i, c := range clusters {
		clusterIDs[i] = c.ID
	}
	fleetMetrics, err := fc.MetricsFederator.FederateMetrics(r.Context(), clusterIDs)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fleetMetrics)
}

// handleModelMetrics returns metrics for a specific model.
func (fc *FleetController) handleModelMetrics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	model := r.PathValue("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}
	modelMetrics, err := fc.MetricsFederator.GetModelMetrics(r.Context(), model)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, modelMetrics)
}

// handleListRollouts returns all rollouts.
func (fc *FleetController) handleListRollouts(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	rollouts, err := fc.RolloutRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rollouts)
}

// rolloutCreateRequest is the JSON body for POST /api/v1/rollouts.
type rolloutCreateRequest struct {
	PoolID       string `json:"pool_id"`
	ModelVersion string `json:"model_version"`
	Strategy     string `json:"strategy"`
}

// handleCreateRollout creates a new rollout.
func (fc *FleetController) handleCreateRollout(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var req rolloutCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.PoolID == "" || req.ModelVersion == "" {
		writeError(w, http.StatusBadRequest, "pool_id and model_version are required")
		return
	}

	record := postgres.RolloutRecord{
		PoolID:        req.PoolID,
		ModelVersion:  req.ModelVersion,
		Strategy:      map[string]interface{}{"type": req.Strategy},
		Status:        "pending",
		CurrentWeight: 0,
	}
	if err := fc.RolloutRepo.Create(r.Context(), record); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rolloutsGauge.Add(1)
	writeJSON(w, http.StatusCreated, map[string]string{
		"status":        "created",
		"pool_id":       req.PoolID,
		"model_version": req.ModelVersion,
	})
}

// handlePromoteRollout promotes a canary rollout.
func (fc *FleetController) handlePromoteRollout(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rollout id is required")
		return
	}
	state, err := fc.RolloutController.AdvanceRollout(r.Context(), id)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleRollbackRollout rolls back a rollout.
func (fc *FleetController) handleRollbackRollout(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rollout id is required")
		return
	}
	state, err := fc.RolloutController.RollbackRollout(r.Context(), id)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleVerifyChains verifies all ledger decision chains.
func (fc *FleetController) handleVerifyChains(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	results, err := fc.FleetRecorder.VerifyAllChains(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// ----------------------------------------------------------------------------
// Cost and pricing handlers
// ----------------------------------------------------------------------------

// handleCostPricing returns the full GPU pricing table as JSON.
func (fc *FleetController) handleCostPricing(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.Add(1)
	writeJSON(w, http.StatusOK, fc.PricingTable.AllPrices())
}

// handleCostTokenomics computes token costs for a model across all GPU types.
func (fc *FleetController) handleCostTokenomics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	model := r.PathValue("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}

	throughput := 1000.0
	if tpStr := r.URL.Query().Get("throughput"); tpStr != "" {
		tp, err := strconv.ParseFloat(tpStr, 64)
		if err != nil || tp <= 0 {
			writeError(w, http.StatusBadRequest, "throughput must be a positive number")
			return
		}
		throughput = tp
	}

	var results []cost.TokenCost
	for _, gpuType := range fc.PricingTable.ListGPUTypes() {
		for _, tier := range fc.PricingTable.ListTiers() {
			tc, err := cost.ComputeTokenCost(model, gpuType, tier, throughput, fc.PricingTable)
			if err != nil {
				continue
			}
			results = append(results, *tc)
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// handleCostChargeback generates a chargeback report for a tenant.
func (fc *FleetController) handleCostChargeback(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	tenant := r.PathValue("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant id is required")
		return
	}

	budget := 10000.0
	if bStr := r.URL.Query().Get("budget"); bStr != "" {
		b, err := strconv.ParseFloat(bStr, 64)
		if err == nil && b > 0 {
			budget = b
		}
	}

	// Build usage records from the metering summary. Since the metering
	// system returns aggregate summaries rather than individual records, we
	// construct a single synthetic usage record per tenant.
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	period := metering.TimePeriod{Start: start, End: now}

	var costUsage []cost.UsageRecord
	meterUsage, err := fc.UsageTracker.GetUsage(r.Context(), tenant, period)
	if err == nil && meterUsage != nil {
		costUsage = append(costUsage, cost.UsageRecord{
			TenantID:  tenant,
			Model:     "aggregate",
			Cluster:   "fleet",
			GPUType:   "H200",
			Tokens:    meterUsage.TokensConsumed,
			Duration:  time.Duration(meterUsage.RequestCount) * time.Second,
			Timestamp: now,
		})
	}

	report := cost.GenerateChargebackReport(tenant, costUsage, fc.PricingTable, budget)
	writeJSON(w, http.StatusOK, report)
}

// handleCostProjection projects monthly cost based on current usage rates.
func (fc *FleetController) handleCostProjection(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	tokensPerDay := int64(10_000_000) // default
	if tdStr := r.URL.Query().Get("tokens_per_day"); tdStr != "" {
		td, err := strconv.ParseInt(tdStr, 10, 64)
		if err != nil || td <= 0 {
			writeError(w, http.StatusBadRequest, "tokens_per_day must be a positive integer")
			return
		}
		tokensPerDay = td
	}

	gpuType := r.URL.Query().Get("gpu_type")
	if gpuType == "" {
		gpuType = "H200"
	}
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "on-demand"
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		model = "default"
	}

	tc, err := cost.ComputeTokenCost(model, gpuType, tier, 1000, fc.PricingTable)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	monthly := cost.ProjectMonthlyCost(tokensPerDay, tc)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"model":          model,
		"gpu_type":       gpuType,
		"tier":           tier,
		"tokens_per_day": tokensPerDay,
		"monthly_cost":   monthly,
		"token_cost":     tc,
	})
}

// handleCostSavings compares current cost versus optimized placement cost.
func (fc *FleetController) handleCostSavings(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	currentMonthly := 5000.0
	if cmStr := r.URL.Query().Get("current"); cmStr != "" {
		cm, err := strconv.ParseFloat(cmStr, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "current must be a number")
			return
		}
		currentMonthly = cm
	}

	optimizedMonthly := 3000.0
	if omStr := r.URL.Query().Get("optimized"); omStr != "" {
		om, err := strconv.ParseFloat(omStr, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "optimized must be a number")
			return
		}
		optimizedMonthly = om
	}

	projection := cost.ProjectSavings(currentMonthly, optimizedMonthly)
	writeJSON(w, http.StatusOK, projection)
}

// handleCostAlerts checks all tenant budgets and returns active alerts.
func (fc *FleetController) handleCostAlerts(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	tenants, err := fc.TenantRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var configs []cost.TenantBudgetConfig
	currentCosts := make(map[string]float64)

	for _, t := range tenants {
		// Extract budget from CostControl if available; default to $10,000.
		budget := 10000.0
		if t.CostControl != nil {
			if b, ok := t.CostControl["monthly_budget"]; ok {
				if bf, ok := b.(float64); ok {
					budget = bf
				}
			}
		}
		configs = append(configs, cost.TenantBudgetConfig{
			TenantID:      t.ID,
			MonthlyBudget: budget,
			WarningAt:     0.8,
			CriticalAt:    0.95,
		})
		// Estimate current cost from metering data.
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		period := metering.TimePeriod{Start: start, End: now}
		usage, err := fc.UsageTracker.GetUsage(r.Context(), t.ID, period)
		if err == nil && usage != nil {
			// Parse cost string from metering summary.
			if costVal, parseErr := strconv.ParseFloat(usage.TotalCost, 64); parseErr == nil {
				currentCosts[t.ID] = costVal
			}
		}
	}

	alerts := cost.CheckBudgetAlerts(configs, currentCosts)
	writeJSON(w, http.StatusOK, alerts)
}

// ----------------------------------------------------------------------------
// ModelPlane handlers
// ----------------------------------------------------------------------------

// handleModelPlaneClusters returns the most recently watched ModelPlane clusters.
func (fc *FleetController) handleModelPlaneClusters(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.Add(1)
	if fc.ModelPlaneWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "ModelPlane integration not configured")
		return
	}
	writeJSON(w, http.StatusOK, fc.ModelPlaneWatcher.LastClusters())
}

// handleModelPlaneDeployments returns the most recently watched ModelPlane deployments.
func (fc *FleetController) handleModelPlaneDeployments(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.Add(1)
	if fc.ModelPlaneWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "ModelPlane integration not configured")
		return
	}
	writeJSON(w, http.StatusOK, fc.ModelPlaneWatcher.LastDeployments())
}

// handleModelPlaneDeploymentCost returns the hourly cost of a ModelPlane deployment.
func (fc *FleetController) handleModelPlaneDeploymentCost(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	if fc.ModelPlaneWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "ModelPlane integration not configured")
		return
	}

	deploymentName := r.PathValue("deployment")
	if deploymentName == "" {
		writeError(w, http.StatusBadRequest, "deployment name is required")
		return
	}

	deployments := fc.ModelPlaneWatcher.LastDeployments()
	clusters := fc.ModelPlaneWatcher.LastClusters()

	var target *modelplane.ModelDeployment
	for i := range deployments {
		if deployments[i].Name == deploymentName {
			target = &deployments[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("deployment %q not found", deploymentName))
		return
	}

	hourlyCost, err := cost.ComputeDeploymentCost(*target, clusters, fc.PricingTable)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deployment":  target.Name,
		"model":       target.Model,
		"replicas":    target.Replicas,
		"hourly_cost": hourlyCost,
	})
}

// ----------------------------------------------------------------------------
// Server setup and lifecycle
// ----------------------------------------------------------------------------

// setupAPIServer creates the main HTTP API server mux. The mode parameter
// controls which routes are mounted: "all" (default) mounts everything,
// "control" mounts only fleet management API routes, and "inference" mounts
// only inference proxy routes. Health probes are always mounted.
func (fc *FleetController) setupAPIServer(mode string) *http.ServeMux {
	mux := http.NewServeMux()

	// Health probes — always mounted
	mux.HandleFunc("GET /healthz", fc.handleHealthz)
	mux.HandleFunc("GET /readyz", fc.handleReadyz)

	// Control plane routes
	if mode == "all" || mode == "control" {
		// Clusters
		mux.HandleFunc("GET /api/v1/clusters", fc.handleListClusters)
		mux.HandleFunc("POST /api/v1/clusters", fc.handleRegisterCluster)
		mux.HandleFunc("DELETE /api/v1/clusters/{id}", fc.handleDeregisterCluster)

		// Pools
		mux.HandleFunc("GET /api/v1/pools", fc.handleListPools)

		// Reconciler webhook (accepts CRD watch events)
		if fc.Reconciler != nil {
			mux.HandleFunc("POST /api/v1/webhook/fleetinferencepool", fc.Reconciler.WatchEndpoint())
			mux.HandleFunc("GET /api/v1/pools/{name}/state", fc.handleGetPoolState)
		}

		// Validation webhook (admission controller)
		mux.HandleFunc("POST /api/v1/webhook/validate", controller.WebhookHandler())

		// Tenants
		mux.HandleFunc("GET /api/v1/tenants", fc.handleListTenants)
		mux.HandleFunc("GET /api/v1/tenants/{id}/usage", fc.handleTenantUsage)

		// Metrics
		mux.HandleFunc("GET /api/v1/metrics/fleet", fc.handleFleetMetrics)
		mux.HandleFunc("GET /api/v1/metrics/model/{model}", fc.handleModelMetrics)

		// Rollouts
		mux.HandleFunc("GET /api/v1/rollouts", fc.handleListRollouts)
		mux.HandleFunc("POST /api/v1/rollouts", fc.handleCreateRollout)
		mux.HandleFunc("POST /api/v1/rollouts/{id}/promote", fc.handlePromoteRollout)
		mux.HandleFunc("POST /api/v1/rollouts/{id}/rollback", fc.handleRollbackRollout)

		// Ledger verification
		mux.HandleFunc("GET /api/v1/verify/chains", fc.handleVerifyChains)

		// Cost and pricing
		mux.HandleFunc("GET /api/v1/cost/pricing", fc.handleCostPricing)
		mux.HandleFunc("GET /api/v1/cost/tokenomics/{model}", fc.handleCostTokenomics)
		mux.HandleFunc("GET /api/v1/cost/chargeback/{tenant}", fc.handleCostChargeback)
		mux.HandleFunc("GET /api/v1/cost/projection", fc.handleCostProjection)
		mux.HandleFunc("GET /api/v1/cost/savings", fc.handleCostSavings)
		mux.HandleFunc("GET /api/v1/cost/alerts", fc.handleCostAlerts)

		// ModelPlane integration
		mux.HandleFunc("GET /api/v1/modelplane/clusters", fc.handleModelPlaneClusters)
		mux.HandleFunc("GET /api/v1/modelplane/deployments", fc.handleModelPlaneDeployments)
		mux.HandleFunc("GET /api/v1/modelplane/cost/{deployment}", fc.handleModelPlaneDeploymentCost)
	}

	// Inference proxy routes
	if mode == "all" || mode == "inference" {
		mux.Handle("POST /v1/chat/completions", fc.InferenceProxy)
		mux.Handle("POST /v1/completions", fc.InferenceProxy)
	}

	return mux
}

// setupMetricsServer creates the metrics HTTP server mux.
func setupMetricsServer() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", expvar.Handler())
	return mux
}

// Run starts the fleet controller HTTP servers and blocks until the context
// is cancelled or a shutdown signal is received.
func (fc *FleetController) Run(ctx context.Context, port, metricsPort int, authCfg auth.Config, tlsCert, tlsKey, mode string, rateLimiter *auth.RateLimiter, rateLimitExempt []string) error {
	// Create a context that is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Wrap the API server mux with auth middleware and rate limiting.
	mux := fc.setupAPIServer(mode)
	exempt := defaultExemptPaths(rateLimitExempt)
	var handler http.Handler = auth.AuthorizationMiddleware(exempt, mux)
	handler = auth.AuthMiddleware(authCfg, exempt, handler)
	if rateLimiter != nil {
		handler = auth.RateLimitMiddlewareWithExemptions(rateLimiter, exempt, handler)
	}

	apiServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 180 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	metricsServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", metricsPort),
		Handler:      setupMetricsServer(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start metrics server.
	go func() {
		log.Printf("metrics server listening on :%d", metricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	// Start API server (with TLS if cert and key are provided).
	go func() {
		if tlsCert != "" && tlsKey != "" {
			log.Printf("api server listening on :%d (TLS enabled)", port)
			if err := apiServer.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
				log.Printf("api server error: %v", err)
			}
		} else {
			log.Printf("api server listening on :%d", port)
			if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("api server error: %v", err)
			}
		}
	}()

	// Start CRD and ModelPlane watchers only when running control plane.
	if mode != "inference" {
		// Start CRD watcher if Kubernetes API is configured.
		if fc.CRDWatcher != nil {
			if err := fc.CRDWatcher.Start(ctx); err != nil {
				log.Printf("WARNING: CRD watcher failed to start: %v", err)
			} else {
				log.Println("CRD watcher started for FleetInferencePool resources")
			}
		}

		// Start ModelPlane watcher if configured.
		if fc.ModelPlaneWatcher != nil {
			if err := fc.ModelPlaneWatcher.Start(ctx); err != nil {
				log.Printf("WARNING: ModelPlane watcher failed to start: %v", err)
			} else {
				log.Println("ModelPlane watcher started")
			}
		}
	}

	// Mark as ready.
	fc.ready.Store(true)
	log.Println("fleet-controller is ready")

	// Wait for shutdown signal.
	<-ctx.Done()
	log.Println("fleet-controller shutting down...")

	// Graceful shutdown with a 15-second deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var shutdownErr error
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		shutdownErr = fmt.Errorf("api server shutdown: %w", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		if shutdownErr != nil {
			shutdownErr = fmt.Errorf("%v; metrics server shutdown: %w", shutdownErr, err)
		} else {
			shutdownErr = fmt.Errorf("metrics server shutdown: %w", err)
		}
	}

	log.Println("fleet-controller stopped")
	return shutdownErr
}

func defaultExemptPaths(configured []string) []string {
	required := []string{"/healthz", "/readyz", "/metrics"}
	seen := make(map[string]bool)
	for _, p := range required {
		seen[p] = true
	}
	merged := append([]string{}, required...)
	for _, p := range configured {
		if !seen[p] {
			merged = append(merged, p)
			seen[p] = true
		}
	}
	return merged
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func main() {
	port := flag.Int("port", 8080, "API server port")
	metricsPort := flag.Int("metrics-port", 9091, "Metrics server port")
	mode := flag.String("mode", "all", "Server mode: all (default), control (fleet API only), inference (inference proxy only)")
	ledgerMode := flag.String("ledger-mode", string(ledger.ModeMemory), "Ledger backend mode: disabled, memory, http, grpc")
	ledgerEndpoint := flag.String("ledger-endpoint", "localhost:9092", "ARE ledger endpoint")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file")
	backendVLLM := flag.String("backend-vllm", "http://vllm-cpu.fleet-llm-d.svc:8000", "Base URL for the vLLM inference backend")
	backendOVMS := flag.String("backend-ovms", "http://ovms-granite-external.fleet-llm-d.svc:8080", "Base URL for the OVMS inference backend")
	kubeAPI := flag.String("kube-api", "", "Kubernetes API server URL (enables CRD watching when set)")
	namespace := flag.String("namespace", "default", "Kubernetes namespace to watch for FleetInferencePool CRDs")
	pgURL := flag.String("pg-url", "", "PostgreSQL connection string (e.g. postgres://user:pass@host:5432/fleet?sslmode=disable). When set, uses PostgreSQL instead of in-memory stores")
	eventEndpoint := flag.String("event-endpoint", "", "HTTP endpoint for publishing fleet events (e.g. http://kafka-bridge:8080/topics/fleet-events). When set, events are also POSTed to this URL")
	modelplaneAPI := flag.String("modelplane-api", "", "ModelPlane API server URL (enables ModelPlane integration when set)")
	modelplaneNamespace := flag.String("modelplane-namespace", "default", "ModelPlane namespace to watch for resources")
	rateLimit := flag.Float64("rate-limit", 100, "Rate limit in requests per second per IP (0 to disable)")
	rateBurst := flag.Int("rate-burst", 200, "Rate limit burst size (max requests before throttling)")
	rateLimitExempt := flag.String("rate-limit-exempt", "/healthz,/readyz,/metrics", "Comma-separated exact paths exempt from rate limiting and auth")
	backends := flag.String("backends", "", `JSON array of inference backends: [{"model":"name","url":"http://...","runtime":"openvino|vllm","path_prefix":"/v3"}]`)
	maxInflight := flag.Int("max-inflight", 0, "Max concurrent inference requests per model (0 = disabled)")
	flag.Parse()

	log.Printf("fleet-controller starting (mode=%s, log-level=%s, ledger-mode=%s, ledger=%s)", *mode, *logLevel, *ledgerMode, *ledgerEndpoint)

	// Build auth config from environment variable FLEET_AUTH_SECRET.
	authCfg := auth.ConfigFromEnv()

	log.Printf("auth enabled=%v, TLS enabled=%v, kube-api=%q, namespace=%q, pg=%v, event-endpoint=%q",
		authCfg.Enabled, *tlsCert != "" && *tlsKey != "", *kubeAPI, *namespace,
		*pgURL != "", *eventEndpoint)

	fc := NewFleetControllerWithLedgerConfig(ledger.Config{
		Mode:     ledger.Mode(*ledgerMode),
		Endpoint: *ledgerEndpoint,
	}, *backendVLLM, *backendOVMS, *kubeAPI, *namespace)

	// Configure per-model load shedding if --max-inflight is set.
	fc.InferenceProxy.SetMaxInflight(*maxInflight)

	// Register additional backends from --backends JSON flag.
	if *backends != "" {
		var backendList []struct {
			Model      string `json:"model"`
			URL        string `json:"url"`
			Runtime    string `json:"runtime"`
			PathPrefix string `json:"path_prefix"`
		}
		if err := json.Unmarshal([]byte(*backends), &backendList); err != nil {
			log.Fatalf("failed to parse --backends JSON: %v", err)
		}
		for _, b := range backendList {
			fc.InferenceProxy.RegisterBackend(b.Model, routing.Backend{
				Name:       fmt.Sprintf("%s-%s", b.Runtime, b.Model),
				URL:        b.URL,
				Runtime:    b.Runtime,
				PathPrefix: b.PathPrefix,
				Healthy:    true,
				LatencyMs:  500,
			})
			log.Printf("registered backend: model=%s url=%s runtime=%s", b.Model, b.URL, b.Runtime)
		}
	}

	// Override stores with PostgreSQL when --pg-url is set.
	if *pgURL != "" {
		db, err := sql.Open("postgres", *pgURL)
		if err != nil {
			log.Fatalf("failed to open postgres: %v", err)
		}
		defer db.Close()

		pgClient := postgres.NewPGClientFromDB(db)
		if err := pgClient.Ping(context.Background()); err != nil {
			log.Fatalf("failed to ping postgres: %v", err)
		}
		log.Println("connected to PostgreSQL — using persistent stores")

		fc.ClusterRepo = postgres.NewPGClusterRepository(pgClient)
		fc.ClusterClient = client.NewRepositoryClusterClient(fc.ClusterRepo)
		fc.PoolRepo = postgres.NewPGFleetPoolRepository(pgClient)
		fc.TenantRepo = postgres.NewPGTenantRepository(pgClient)
		fc.RolloutRepo = postgres.NewPGRolloutRepository(pgClient)
	}

	// Initialize clustersGauge from existing data so the gauge reflects
	// clusters that were persisted before this process started.
	if clusters, err := fc.ClusterClient.ListClusters(context.Background()); err == nil {
		clustersGauge.Add(int64(len(clusters)))
	}

	// Override event publisher with HTTP publisher when --event-endpoint is set.
	if *eventEndpoint != "" {
		fc.EventPublisher = events.NewHTTPEventPublisher(*eventEndpoint)
		log.Printf("event publishing enabled (endpoint=%s)", *eventEndpoint)
	}

	// Wire ModelPlane integration when --modelplane-api is set.
	if *modelplaneAPI != "" {
		mpToken := ""
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			mpToken = string(data)
		}
		watcher := modelplane.NewModelPlaneWatcher(*modelplaneAPI, *modelplaneNamespace, mpToken)
		bridge := modelplane.NewComplianceBridge(fc.FleetRecorder)

		watcher.OnClusterChange(func(clusters []modelplane.InferenceCluster) {
			for _, c := range clusters {
				if _, err := bridge.RecordClusterProvisioned(context.Background(), c); err != nil {
					log.Printf("failed to record cluster provisioned %s: %v", c.Name, err)
				}
			}
		})
		watcher.OnDeploymentChange(func(deployments []modelplane.ModelDeployment) {
			for _, d := range deployments {
				if _, err := bridge.RecordDeploymentCreated(context.Background(), d); err != nil {
					log.Printf("failed to record deployment created %s: %v", d.Name, err)
				}
			}
		})
		watcher.OnEndpointChange(func(endpoints []modelplane.ModelEndpoint) {
			for _, e := range endpoints {
				if _, err := bridge.RecordEndpointReady(context.Background(), e); err != nil {
					log.Printf("failed to record endpoint ready %s: %v", e.Name, err)
				}
			}
		})

		fc.ModelPlaneWatcher = watcher
		fc.ModelPlaneBridge = bridge
		log.Printf("ModelPlane integration enabled (api=%s, namespace=%s)", *modelplaneAPI, *modelplaneNamespace)
	}

	// Create rate limiter when rate limiting is enabled.
	var rl *auth.RateLimiter
	if *rateLimit > 0 {
		rl = auth.NewRateLimiter(*rateLimit, *rateBurst)
		log.Printf("rate limiting enabled (rate=%.0f/s, burst=%d)", *rateLimit, *rateBurst)
	}

	if err := fc.Run(context.Background(), *port, *metricsPort, authCfg, *tlsCert, *tlsKey, *mode, rl, splitCSV(*rateLimitExempt)); err != nil {
		log.Fatal(err)
	}
}
