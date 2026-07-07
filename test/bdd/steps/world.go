//go:build bdd

package steps

import (
	"context"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/kvcache/transfer"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/lifecycle/rollout"
	"github.com/llm-d/fleet-llm-d/pkg/observability/metrics"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/quota"
)

// ClusterState holds the state of a cluster in the test world.
type ClusterState struct {
	Info         solver.ClusterInfo
	Healthy      bool
	LatencyMs    float64
	CostPerGPUHour float64
	KVCacheHitRate float64
	CostPerToken float64
	Region       string
	KVCacheSize  string
	Replicas     int
	GPUsPerReplica int
	GPUUtilization float64
	TTFT_P99_Ms  float64
	Throughput   float64
	PoolMetrics  collector.PoolMetrics
}

// PoolState holds FleetInferencePool state.
type PoolState struct {
	Spec          v1alpha1.FleetInferencePoolSpec
	Policy        v1alpha1.PlacementPolicySpec
	RoutingPolicy v1alpha1.FleetRoutingPolicySpec
	ScalingPolicy v1alpha1.FleetScalingPolicySpec
	Replicas      int
	MinClusters   int
	Clusters      []string
}

// TenantState holds tenant profile state.
type TenantState struct {
	Spec           v1alpha1.TenantProfileSpec
	TokensConsumed int64
	CostAccumulated float64
	AllowedClusters []string
	Priority       int
}

// RolloutState holds rollout state for lifecycle tests.
type RolloutTestState struct {
	Lifecycle v1alpha1.ModelLifecycleSpec
	State     *rollout.RolloutState
}

// LedgerEntry represents a recorded ledger entry.
type LedgerEntry struct {
	Type          string
	Receipt       *ledger.LedgerReceipt
	ProofReceipt  *ledger.ProofReceipt
	Content       map[string]interface{}
}

// PlacementResult holds a placement decision result.
type PlacementResult struct {
	Decision solver.PlacementDecision
}

// RouteDecisionResult holds a routing decision result.
type RouteDecisionResult struct {
	Decision policy.RouteDecision
	Request  policy.RoutingRequest
}

// ScalingResult holds a scaling action result.
type ScalingResult struct {
	Actions []optimizer.ScalingAction
}

// TransferResult holds a transfer job result.
type TransferResult struct {
	Job *transfer.TransferJob
}

// World holds shared state across BDD steps.
type World struct {
	Ctx             context.Context
	Clusters        map[string]*ClusterState
	FleetPools      map[string]*PoolState
	Tenants         map[string]*TenantState
	Rollouts        map[string]*RolloutTestState
	LedgerEntries   []LedgerEntry
	LastError       error
	LastPlacement   []PlacementResult
	LastRouteDecision *RouteDecisionResult
	LastScaling     *ScalingResult
	LastTransfer    *TransferResult

	// References to capability packages
	Solver          solver.ConstraintSolver
	Evaluator       policy.RoutingPolicyEvaluator
	Optimizer       optimizer.FleetOptimizer
	QuotaEnforcer   quota.QuotaEnforcer
	Collector       *collector.InMemoryCollector
	Rollout         rollout.RolloutController
	Federator       metrics.MetricsFederator
	Orchestrator    transfer.TransferOrchestrator
	Recorder        *ledger.FleetRecorder

	// Quota check results
	LastQuotaResult *quota.QuotaCheckResult

	// Events collected during test
	Events []string
}

// NewWorld creates a new World with all capability instances initialized.
func NewWorld() *World {
	lc := ledger.NewInMemoryLedgerClient()
	rec := ledger.NewFleetRecorder(lc, "test-agent", "bdd-test")
	col := collector.NewMetricsCollector()

	return &World{
		Ctx:           context.Background(),
		Clusters:      make(map[string]*ClusterState),
		FleetPools:    make(map[string]*PoolState),
		Tenants:       make(map[string]*TenantState),
		Rollouts:      make(map[string]*RolloutTestState),
		LedgerEntries: []LedgerEntry{},
		Solver:        solver.NewConstraintSolver(),
		Evaluator:     policy.NewRoutingPolicyEvaluator(),
		Optimizer:     optimizer.NewFleetOptimizer(),
		QuotaEnforcer: quota.NewQuotaEnforcer(),
		Collector:     col.(*collector.InMemoryCollector),
		Rollout:       rollout.NewRolloutController(),
		Federator:     metrics.NewMetricsFederator(),
		Orchestrator:  transfer.NewTransferOrchestrator(),
		Recorder:      rec,
		Events:        []string{},
	}
}
