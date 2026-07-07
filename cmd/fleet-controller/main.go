package main

import (
	"context"
	"encoding/json"
	"expvar"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/kvcache/transfer"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/lifecycle/rollout"
	"github.com/llm-d/fleet-llm-d/pkg/observability/metrics"
	"github.com/llm-d/fleet-llm-d/pkg/placement/scorer"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
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

	// Ledger integration
	FleetRecorder *ledger.FleetRecorder

	// Repositories for CRUD operations
	PoolRepo    postgres.FleetPoolRepository
	TenantRepo  postgres.TenantRepository
	RolloutRepo postgres.RolloutRepository

	// Server state
	ready atomic.Bool
}

// NewFleetController creates a new FleetController with all components
// initialized using their default constructors.
func NewFleetController(ledgerEndpoint string) *FleetController {
	ledgerClient := ledger.NewLedgerClient(ledgerEndpoint)
	return &FleetController{
		Solver: solver.NewConstraintSolver(),
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
		ClusterClient:        client.NewMultiClusterClient(),
		EventPublisher:       events.NewEventPublisher(),
		FleetRecorder:        ledger.NewFleetRecorder(ledgerClient, "fleet-controller", "fleet-llm-d"),
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
	if err := fc.ClusterClient.RegisterCluster(r.Context(), reg); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clustersGauge.Add(1)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered", "id": req.ID})
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

// handleListPools returns all fleet inference pools.
func (fc *FleetController) handleListPools(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	pools, err := fc.PoolRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pools)
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
// Server setup and lifecycle
// ----------------------------------------------------------------------------

// setupAPIServer creates the main HTTP API server mux.
func (fc *FleetController) setupAPIServer() *http.ServeMux {
	mux := http.NewServeMux()

	// Health probes
	mux.HandleFunc("GET /healthz", fc.handleHealthz)
	mux.HandleFunc("GET /readyz", fc.handleReadyz)

	// Clusters
	mux.HandleFunc("GET /api/v1/clusters", fc.handleListClusters)
	mux.HandleFunc("POST /api/v1/clusters", fc.handleRegisterCluster)
	mux.HandleFunc("DELETE /api/v1/clusters/{id}", fc.handleDeregisterCluster)

	// Pools
	mux.HandleFunc("GET /api/v1/pools", fc.handleListPools)

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
func (fc *FleetController) Run(ctx context.Context, port, metricsPort int) error {
	// Create a context that is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	apiServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      fc.setupAPIServer(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
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

	// Start API server.
	go func() {
		log.Printf("api server listening on :%d", port)
		if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api server error: %v", err)
		}
	}()

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

func main() {
	port := flag.Int("port", 8080, "API server port")
	metricsPort := flag.Int("metrics-port", 9090, "Metrics server port")
	ledgerEndpoint := flag.String("ledger-endpoint", "localhost:9092", "ARE ledger gRPC endpoint")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	log.Printf("fleet-controller starting (log-level=%s, ledger=%s)", *logLevel, *ledgerEndpoint)

	fc := NewFleetController(*ledgerEndpoint)
	if err := fc.Run(context.Background(), *port, *metricsPort); err != nil {
		log.Fatal(err)
	}
}
