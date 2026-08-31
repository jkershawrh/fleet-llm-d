//go:build bdd

package steps

import (
	"fmt"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

// SetRoutingPolicy creates a FleetRoutingPolicy in the world.
func (w *World) SetRoutingPolicy(policyName, strategy string) {
	if _, ok := w.FleetPools[policyName]; !ok {
		w.FleetPools[policyName] = &PoolState{}
	}
	w.FleetPools[policyName].RoutingPolicy = v1alpha1.FleetRoutingPolicySpec{
		Strategy: strategy,
	}
}

// AddRoutingRule adds a routing rule to a named policy.
func (w *World) AddRoutingRule(policyName string, rule v1alpha1.RoutingRule) {
	if pool, ok := w.FleetPools[policyName]; ok {
		pool.RoutingPolicy.Rules = append(pool.RoutingPolicy.Rules, rule)
	}
}

// SetClusterHealth sets the health and metrics for a cluster.
func (w *World) SetClusterHealth(name string, healthy bool, latencyMs float64, kvCacheHitRate float64, costPerToken float64) {
	if cs, ok := w.Clusters[name]; ok {
		cs.Healthy = healthy
		cs.LatencyMs = latencyMs
		cs.KVCacheHitRate = kvCacheHitRate
		cs.CostPerToken = costPerToken
	}
}

// SetClusterUnhealthy marks a cluster as unhealthy.
func (w *World) SetClusterUnhealthy(name string) {
	if cs, ok := w.Clusters[name]; ok {
		cs.Healthy = false
	}
}

// SetClusterHealthy marks a cluster as healthy.
func (w *World) SetClusterHealthy(name string) {
	if cs, ok := w.Clusters[name]; ok {
		cs.Healthy = true
	}
}

// BuildClusterHealthList creates a slice of ClusterHealth from the world state.
func (w *World) BuildClusterHealthList() []policy.ClusterHealth {
	var result []policy.ClusterHealth
	for name, cs := range w.Clusters {
		result = append(result, policy.ClusterHealth{
			ClusterID:         name,
			Healthy:           cs.Healthy,
			LatencyMs:         cs.LatencyMs,
			CapacityRemaining: 1.0 - cs.Info.Utilization,
			KVCacheHitRate:    cs.KVCacheHitRate,
			AvailableSlots:    cs.Info.GPUCapacity.Available,
			CurrentLoad:       cs.Info.Utilization,
			CostPerToken:      cs.CostPerToken,
			Region:            cs.Region,
		})
	}
	return result
}

// EvaluateRouting runs the routing policy evaluator.
func (w *World) EvaluateRouting(policyName string, request policy.RoutingRequest) error {
	policyPool, ok := w.FleetPools[policyName]
	if !ok {
		return fmt.Errorf("routing policy %q not found", policyName)
	}

	clusters := w.BuildClusterHealthList()
	decision, err := w.Evaluator.Evaluate(w.Ctx, request, clusters, policyPool.RoutingPolicy)
	if err != nil {
		w.LastError = err
		w.LastRouteDecision = nil
		return nil
	}

	w.LastRouteDecision = &RouteDecisionResult{
		Decision: decision,
		Request:  request,
	}
	w.LastError = nil
	return nil
}

// AssertRoutedTo checks the routing target cluster.
func (w *World) AssertRoutedTo(expected string) error {
	if w.LastRouteDecision == nil {
		return fmt.Errorf("no routing decision available")
	}
	if w.LastRouteDecision.Decision.TargetCluster != expected {
		return fmt.Errorf("expected routing to %q, got %q", expected, w.LastRouteDecision.Decision.TargetCluster)
	}
	return nil
}

// AssertRoutedToOneOf checks that routing target is one of the expected clusters.
func (w *World) AssertRoutedToOneOf(candidates []string) error {
	if w.LastRouteDecision == nil {
		return fmt.Errorf("no routing decision available")
	}
	target := w.LastRouteDecision.Decision.TargetCluster
	for _, c := range candidates {
		if target == c {
			return nil
		}
	}
	return fmt.Errorf("routed to %q, expected one of %v", target, candidates)
}

// AssertNotRoutedTo checks that routing did NOT go to a specific cluster.
func (w *World) AssertNotRoutedTo(excluded string) error {
	if w.LastRouteDecision == nil {
		return fmt.Errorf("no routing decision available")
	}
	if w.LastRouteDecision.Decision.TargetCluster == excluded {
		return fmt.Errorf("should not route to %q, but did", excluded)
	}
	return nil
}

// AssertRoutingReason checks the routing decision reason.
func (w *World) AssertRoutingReason(expected string) error {
	if w.LastRouteDecision == nil {
		return fmt.Errorf("no routing decision available")
	}
	actual := w.LastRouteDecision.Decision.Reason
	if !strings.Contains(actual, expected) && actual != expected {
		// Accept partial matches since reason strings may vary
		return fmt.Errorf("expected routing reason %q, got %q", expected, actual)
	}
	return nil
}

// AssertLatencyBelow checks that the target cluster latency is below a threshold.
func (w *World) AssertLatencyBelow(maxMs float64) error {
	if w.LastRouteDecision == nil {
		return fmt.Errorf("no routing decision available")
	}
	target := w.LastRouteDecision.Decision.TargetCluster
	cs, ok := w.Clusters[target]
	if !ok {
		return fmt.Errorf("target cluster %q not found", target)
	}
	if cs.LatencyMs > maxMs {
		return fmt.Errorf("latency %.1fms exceeds max %.1fms", cs.LatencyMs, maxMs)
	}
	return nil
}

// AssertLowestCost checks that the target cluster has the lowest cost per token.
func (w *World) AssertLowestCost() error {
	if w.LastRouteDecision == nil {
		return fmt.Errorf("no routing decision available")
	}
	target := w.LastRouteDecision.Decision.TargetCluster
	targetCS, ok := w.Clusters[target]
	if !ok {
		return fmt.Errorf("target cluster %q not found", target)
	}
	for name, cs := range w.Clusters {
		if name != target && cs.Healthy && cs.CostPerToken < targetCS.CostPerToken {
			return fmt.Errorf("cluster %q (cost %.4f) is cheaper than target %q (cost %.4f)", name, cs.CostPerToken, target, targetCS.CostPerToken)
		}
	}
	return nil
}
