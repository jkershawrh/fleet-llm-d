package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/cost"
	"github.com/llm-d/fleet-llm-d/pkg/intents"
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

	// LeaderElector coordinates active/passive control-plane ownership in Kubernetes.
	LeaderElector *controller.LeaderElector

	// Ledger integration
	FleetRecorder      *ledger.FleetRecorder
	LedgerGatewayURL   string
	LedgerGatewayToken string

	// IntentService owns honest asynchronous intent/operation semantics.
	IntentService *intents.Service

	// DecisionPackageDecoder verifies producer-owned GCL CloudEvents before
	// they are projected into FleetIntent admission.
	DecisionPackageDecoder *intents.GCLDecisionPackageDecoder

	// AllowOperatorJSONIntents enables the unsigned, self-asserted JSON v2
	// compatibility input. It is development/operator tooling only and is
	// deliberately false unless explicitly enabled.
	AllowOperatorJSONIntents bool

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
	ModelPlaneWatcher *modelplane.ModelPlaneWatcher
	ModelPlaneBridge  *modelplane.ComplianceBridge

	// Auth secret for token refresh
	AuthSecret string

	// Server state
	ready atomic.Bool
}

// NewFleetController creates a new FleetController with all components
// initialized using their default constructors. The backendVLLM and backendOVMS
// parameters specify the base URLs for the default inference backends. The
// kubeAPI and namespace parameters are optional; when kubeAPI is non-empty a
// CRDWatcher polls FleetInferencePool resources and FleetIntent/FleetOperation
// CRDs become the authoritative intent repository.
func NewFleetController(ledgerEndpoint, backendVLLM, backendOVMS, kubeAPI, namespace string) *FleetController {
	controller, err := NewFleetControllerWithLedgerConfig(ledger.Config{Mode: ledger.ModeMemory, Endpoint: ledgerEndpoint}, backendVLLM, backendOVMS, kubeAPI, namespace)
	if err != nil {
		panic(err)
	}
	return controller
}

// NewFleetControllerWithLedgerConfig creates a FleetController with an
// explicit ledger backend configuration.
func NewFleetControllerWithLedgerConfig(ledgerCfg ledger.Config, backendVLLM, backendOVMS, kubeAPI, namespace string) (*FleetController, error) {
	ledgerClient, err := ledger.NewLedgerClientWithConfig(ledgerCfg)
	if err != nil {
		return nil, fmt.Errorf("initialize immutable-ledger client in %q mode: %w", ledgerCfg.Mode, err)
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

	// Semantic routing: classify prompts and route model="auto" to the right tier.
	// GCL classify-prompt endpoint serves as the classifier.
	semanticRouterURL := os.Getenv("SEMANTIC_CLASSIFIER_URL")
	if semanticRouterURL == "" {
		semanticRouterURL = "http://gcl-app.governed-cognitive-loop.svc:8000"
	}
	proxy.SemanticRouter = routing.NewSemanticRouter(semanticRouterURL, map[string]string{
		"simple":   "granite-3.2-sovereign",
		"standard": "granite-3.2-sovereign",
		"complex":  "granite-3.2-sovereign",
	})

	clusterRepo := postgres.NewInMemoryClusterRepository()
	clusterClient := client.NewRepositoryClusterClient(clusterRepo)
	fleetRecorder := ledger.NewFleetRecorder(ledgerClient, "fleet-controller", "fleet-llm-d")
	constraintSolver := solver.NewConstraintSolver()

	// Create reconciler wired to the cluster client and constraint solver.
	reconciler := controller.NewReconciler(constraintSolver, clusterClient.ListClusters)

	// Wire the onChange callback so every placement decision is recorded
	// to the standalone immutable ledger.
	reconciler.SetOnChange(func(pool *controller.FleetPoolState) {
		for _, clusterID := range pool.DesiredClusters {
			if _, err := fleetRecorder.RecordPlacement(
				context.Background(),
				pool.Model, clusterID, 1, "", "reconciler placement",
			); err != nil {
				slog.Warn("failed to record placement", "model", pool.Model, "cluster", clusterID, "error", err)
			}
		}
	})

	// Create CRDWatcher if Kubernetes API is configured.
	var crdWatcher *controller.CRDWatcher
	var intentRepository intents.Repository = intents.NewMemoryRepository()
	if kubeAPI != "" {
		if namespace == "" {
			namespace = "default"
		}
		// Read service account token for in-cluster auth.
		token := ""
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			token = strings.TrimSpace(string(data))
		}
		crdWatcher = controller.NewCRDWatcher(kubeAPI, namespace, token, reconciler)
		intentRepository = intents.NewKubernetesRepository(kubeAPI, namespace, token, nil)
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
		EventPublisher:       events.NewLedgerAwarePublisher(events.NewEventPublisher(), fleetRecorder),
		PricingTable:         cost.DefaultPricingTable(),
		Reconciler:           reconciler,
		CRDWatcher:           crdWatcher,
		FleetRecorder:        fleetRecorder,
		LedgerGatewayURL: func() string {
			if ledgerCfg.Mode == ledger.ModeHTTP {
				return strings.TrimRight(ledgerCfg.Endpoint, "/")
			}
			return ""
		}(),
		LedgerGatewayToken: ledgerCfg.APIToken,
		IntentService:      intents.NewService(intentRepository, intents.DefaultPolicyConfig(), ledgerClient),
		InferenceProxy:     proxy,
		ClusterRepo:        clusterRepo,
		PoolRepo:           postgres.NewInMemoryFleetPoolRepository(),
		TenantRepo:         postgres.NewInMemoryTenantRepository(),
		RolloutRepo:        postgres.NewInMemoryRolloutRepository(),
	}, nil
}

// BuildClusterHealth assembles routing-ready ClusterHealth entries by combining
// cluster records from the repository with live metrics from the collector.
func (fc *FleetController) BuildClusterHealth(ctx context.Context) []policy.ClusterHealth {
	clusters, err := fc.ClusterRepo.List(ctx)
	if err != nil {
		return nil
	}
	allMetrics, _ := fc.MetricsCollector.CollectAll(ctx)
	metricsMap := make(map[string]collector.PoolMetrics)
	for _, cm := range allMetrics {
		if len(cm.Pools) > 0 {
			metricsMap[cm.ClusterID] = cm.Pools[0]
		}
	}

	var result []policy.ClusterHealth
	for _, c := range clusters {
		ch := policy.ClusterHealth{
			ClusterID:         c.ID,
			Healthy:           c.Status == "Running" || c.Status == "Healthy",
			AvailableSlots:    c.GPUAvailable,
			CapacityRemaining: float64(c.GPUAvailable) / float64(max(c.GPUTotal, 1)),
			Region:            c.Region,
		}
		if pm, ok := metricsMap[c.ID]; ok {
			ch.KVCacheHitRate = pm.KVCacheHitRate
			ch.LatencyMs = pm.TTFT_P99_Ms
			ch.CurrentLoad = pm.GPUUtilization
		}
		result = append(result, ch)
	}
	return result
}
