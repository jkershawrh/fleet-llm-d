package server

import (
	"net/http"
	"os"

	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/observability/metrics"
)

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
	if fc.LeaderElector != nil && !fc.LeaderElector.IsLeader() {
		writeError(w, http.StatusServiceUnavailable, "standby: not the elected leader")
		return
	}
	if fc.CRDWatcher != nil && !fc.CRDWatcher.Ready() {
		writeError(w, http.StatusServiceUnavailable, "Kubernetes fleet API is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// SetupRoutes creates the main HTTP API server mux. The mode parameter
// controls which routes are mounted: "all" (default) mounts everything,
// "control" mounts only fleet management API routes, and "inference" mounts
// only inference proxy routes. Health probes are always mounted.
func (fc *FleetController) SetupRoutes(mode string) *http.ServeMux {
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

		// Per-cluster agent ingestion
		mux.HandleFunc("POST /api/v1/agent/status", fc.handleAgentStatus)
		mux.HandleFunc("POST /api/v1/agent/metrics", fc.handleAgentMetrics)
		mux.HandleFunc("POST /api/v1/agent/events", fc.handleAgentEvent)

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

		// Auth
		mux.HandleFunc("POST /api/v1/auth/refresh", fc.handleRefreshToken)

		// ModelPlane integration
		mux.HandleFunc("GET /api/v1/modelplane/clusters", fc.handleModelPlaneClusters)
		mux.HandleFunc("GET /api/v1/modelplane/deployments", fc.handleModelPlaneDeployments)
		mux.HandleFunc("GET /api/v1/modelplane/cost/{deployment}", fc.handleModelPlaneDeploymentCost)
	}

	// Platform metrics (unified view across all systems)
	if mode == "all" || mode == "control" {
		gclURL := os.Getenv("GCL_URL")
		if gclURL == "" {
			gclURL = "http://gcl-app.governed-cognitive-loop.svc:8000"
		}
		deepfieldURL := os.Getenv("DEEPFIELD_URL")
		if deepfieldURL == "" {
			deepfieldURL = "http://deepfield-fleet.fleet-llm-d.svc:8000"
		}
		ledgerURL := os.Getenv("LEDGER_GATEWAY_URL")
		if ledgerURL == "" {
			ledgerURL = fc.LedgerGatewayURL
		}
		platformCollector := &metrics.PlatformCollector{
			GCLURL:       gclURL,
			DeepfieldURL: deepfieldURL,
			LedgerURL:    ledgerURL,
			LedgerToken:  fc.LedgerGatewayToken,
			ClustersFunc: func() int { return int(clustersGauge.Value()) },
			PoolsFunc:    func() int { return int(poolsGauge.Value()) },
			TenantsFunc:  func() int { return int(tenantsGauge.Value()) },
		}
		mux.HandleFunc("GET /api/v1/metrics/platform", metrics.HandlePlatformMetrics(platformCollector))
	}

	// Inference proxy routes
	if mode == "all" || mode == "inference" {
		mux.Handle("POST /v1/chat/completions", fc.InferenceProxy)
		mux.Handle("POST /v1/completions", fc.InferenceProxy)
	}

	// Intents (predictive brain)
	if mode == "all" || mode == "control" || mode == "inference" {
		mux.HandleFunc("POST /api/v1/intents", fc.handleIntent)
		mux.HandleFunc("POST /api/v2/intents", fc.handleSubmitIntentV2)
		mux.HandleFunc("GET /api/v2/intents/{id}", fc.handleGetIntentV2)
		mux.HandleFunc("GET /api/v2/operations/{id}", fc.handleGetOperationV2)
		mux.HandleFunc("POST /api/v2/operations/{id}/approve", fc.handleApproveOperationV2)
		mux.HandleFunc("POST /api/v2/operations/{id}/cancel", fc.handleCancelOperationV2)
	}

	return mux
}
