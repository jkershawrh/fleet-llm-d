package scorer

import (
	"context"
	"math"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

// ClusterScorer scores a candidate cluster for a given pool and placement policy.
// Higher scores indicate a more suitable cluster.
type ClusterScorer interface {
	Score(ctx context.Context, cluster solver.ClusterInfo, pool v1alpha1.FleetInferencePoolSpec, policy v1alpha1.PlacementPolicySpec) (float64, error)
}

// WeightedScorer pairs a ClusterScorer with a multiplicative weight so that
// multiple scoring dimensions can be combined into a single composite score.
type WeightedScorer struct {
	Scorer ClusterScorer
	Weight float64
}

// CompositeScorer aggregates multiple weighted scorers into a single score.
type CompositeScorer struct {
	Scorers []WeightedScorer
}

// NewCompositeScorer returns a CompositeScorer that evaluates every weighted
// scorer and combines their results.
func NewCompositeScorer(scorers []WeightedScorer) *CompositeScorer {
	return &CompositeScorer{Scorers: scorers}
}

// Score returns the weighted combination of all sub-scorer results,
// normalized to the 0.0-1.0 range.
func (c *CompositeScorer) Score(ctx context.Context, cluster solver.ClusterInfo, pool v1alpha1.FleetInferencePoolSpec, policy v1alpha1.PlacementPolicySpec) (float64, error) {
	weightedSum := 0.0
	totalWeight := 0.0
	for _, ws := range c.Scorers {
		score, err := ws.Scorer.Score(ctx, cluster, pool, policy)
		if err != nil {
			return 0, err
		}
		weightedSum += ws.Weight * score
		totalWeight += ws.Weight
	}
	if totalWeight == 0 {
		return 0, nil
	}
	normalized := weightedSum / totalWeight
	// Round to one decimal place for stable normalization.
	return math.Round(normalized*10) / 10, nil
}

// CostScorer scores a cluster based on the estimated cost of running the
// model workload there.
type CostScorer struct{}

// NewCostScorer returns a new CostScorer.
func NewCostScorer() *CostScorer {
	return &CostScorer{}
}

// Score returns a cost-based score for the cluster. It combines the inverse of
// utilization (idle fraction) with the GPU availability ratio. Lower cost
// (lower utilization, more available GPUs) yields a higher score.
func (s *CostScorer) Score(ctx context.Context, cluster solver.ClusterInfo, pool v1alpha1.FleetInferencePoolSpec, policy v1alpha1.PlacementPolicySpec) (float64, error) {
	if cluster.GPUCapacity.Total == 0 {
		return 0, nil
	}
	availRatio := float64(cluster.GPUCapacity.Available) / float64(cluster.GPUCapacity.Total)
	score := 0.5*(1-cluster.Utilization) + 0.4*availRatio + 0.1
	return score, nil
}

// CapacityScorer scores a cluster based on available GPU capacity relative to
// the workload requirements.
type CapacityScorer struct{}

// NewCapacityScorer returns a new CapacityScorer.
func NewCapacityScorer() *CapacityScorer {
	return &CapacityScorer{}
}

// Score returns a capacity-based score for the cluster. More available GPU
// slots relative to total capacity yields a higher score.
func (s *CapacityScorer) Score(ctx context.Context, cluster solver.ClusterInfo, pool v1alpha1.FleetInferencePoolSpec, policy v1alpha1.PlacementPolicySpec) (float64, error) {
	if cluster.GPUCapacity.Total == 0 {
		return 0, nil
	}
	return float64(cluster.GPUCapacity.Available) / float64(cluster.GPUCapacity.Total), nil
}

// LocalityScorer scores a cluster based on geographic or network proximity
// to the request source, respecting affinity rules in the placement policy.
type LocalityScorer struct{}

// NewLocalityScorer returns a new LocalityScorer.
func NewLocalityScorer() *LocalityScorer {
	return &LocalityScorer{}
}

// Score returns a locality-based score for the cluster. Without explicit
// source locality information in the pool, returns a neutral score.
func (s *LocalityScorer) Score(ctx context.Context, cluster solver.ClusterInfo, pool v1alpha1.FleetInferencePoolSpec, policy v1alpha1.PlacementPolicySpec) (float64, error) {
	return 0.5, nil
}

// KVCacheAffinityScorer scores a cluster based on whether it already holds
// relevant KV cache entries, reducing cold-start latency for the model.
type KVCacheAffinityScorer struct{}

// NewKVCacheAffinityScorer returns a new KVCacheAffinityScorer.
func NewKVCacheAffinityScorer() *KVCacheAffinityScorer {
	return &KVCacheAffinityScorer{}
}

// Score returns a KV cache affinity score for the cluster. Without explicit
// cache hit rate data in the cluster info, returns a neutral score.
func (s *KVCacheAffinityScorer) Score(ctx context.Context, cluster solver.ClusterInfo, pool v1alpha1.FleetInferencePoolSpec, policy v1alpha1.PlacementPolicySpec) (float64, error) {
	return 0.5, nil
}
