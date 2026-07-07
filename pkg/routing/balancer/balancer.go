package balancer

import (
	"context"
	"fmt"

	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

// LoadBalancer defines the interface for selecting a target cluster
// from a set of candidates based on a routing request.
type LoadBalancer interface {
	SelectCluster(ctx context.Context, candidates []policy.ClusterHealth, request policy.RoutingRequest) (string, error)
}

// WeightedBalancer selects clusters using weighted capacity distribution.
type WeightedBalancer struct{}

// NewWeightedBalancer creates a new WeightedBalancer.
func NewWeightedBalancer() *WeightedBalancer {
	return &WeightedBalancer{}
}

// SelectCluster selects the cluster with the highest remaining capacity.
func (b *WeightedBalancer) SelectCluster(ctx context.Context, candidates []policy.ClusterHealth, request policy.RoutingRequest) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no candidates provided")
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.CapacityRemaining > best.CapacityRemaining {
			best = c
		}
	}
	return best.ClusterID, nil
}

// LatencyAwareBalancer selects clusters that minimize request latency.
type LatencyAwareBalancer struct{}

// NewLatencyAwareBalancer creates a new LatencyAwareBalancer.
func NewLatencyAwareBalancer() *LatencyAwareBalancer {
	return &LatencyAwareBalancer{}
}

// SelectCluster selects the cluster with the lowest observed latency.
func (b *LatencyAwareBalancer) SelectCluster(ctx context.Context, candidates []policy.ClusterHealth, request policy.RoutingRequest) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no candidates provided")
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.LatencyMs < best.LatencyMs {
			best = c
		}
	}
	return best.ClusterID, nil
}

// CostAwareBalancer selects clusters that minimize inference cost.
type CostAwareBalancer struct{}

// NewCostAwareBalancer creates a new CostAwareBalancer.
func NewCostAwareBalancer() *CostAwareBalancer {
	return &CostAwareBalancer{}
}

// SelectCluster selects the most cost-effective cluster. When explicit
// CostPerToken data is unavailable, a composite score of remaining capacity
// and KV-cache hit rate is used as a cost-efficiency proxy (higher is better).
func (b *CostAwareBalancer) SelectCluster(ctx context.Context, candidates []policy.ClusterHealth, request policy.RoutingRequest) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no candidates provided")
	}

	best := candidates[0]
	bestScore := costScore(best)
	for _, c := range candidates[1:] {
		s := costScore(c)
		if s > bestScore {
			best = c
			bestScore = s
		}
	}
	return best.ClusterID, nil
}

// costScore returns a cost-efficiency score for a cluster. Higher means
// cheaper/more efficient. It combines remaining capacity (more room = less
// contention cost) with KV-cache hit rate (higher reuse = fewer redundant
// computations).
func costScore(c policy.ClusterHealth) float64 {
	return c.CapacityRemaining + c.KVCacheHitRate
}
