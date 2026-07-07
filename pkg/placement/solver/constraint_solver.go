package solver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/modelpack"
)

// GPUCapacity describes the GPU resources available on a cluster.
type GPUCapacity struct {
	Available int
	Total     int
	Types     []string
}

// ClusterInfo holds the metadata and resource state for a candidate cluster.
type ClusterInfo struct {
	ID          string
	Name        string
	Region      string
	Labels      map[string]string
	GPUCapacity GPUCapacity
	Utilization float64
}

// PlacementDecision records the solver's recommendation for a single cluster.
type PlacementDecision struct {
	ClusterID string
	Replicas  int
	GPUType   string
	Score     float64
	Reasons   []string
}

// ConstraintSolver evaluates placement constraints and produces a set of
// cluster placement decisions for a fleet inference pool.
type ConstraintSolver interface {
	Solve(ctx context.Context, pool v1alpha1.FleetInferencePoolSpec, clusters []ClusterInfo, policy v1alpha1.PlacementPolicySpec) ([]PlacementDecision, error)
}

// defaultConstraintSolver is the built-in implementation of ConstraintSolver.
type defaultConstraintSolver struct{}

// NewConstraintSolver returns a new ConstraintSolver using the default
// implementation.
func NewConstraintSolver() ConstraintSolver {
	return &defaultConstraintSolver{}
}

// Solve filters clusters by constraints, scores them using affinity rules,
// and returns placement decisions sorted by score descending.
func (s *defaultConstraintSolver) Solve(ctx context.Context, pool v1alpha1.FleetInferencePoolSpec, clusters []ClusterInfo, policy v1alpha1.PlacementPolicySpec) ([]PlacementDecision, error) {
	// Filter clusters by constraints.
	eligible := make([]ClusterInfo, len(clusters))
	copy(eligible, clusters)

	var requestedGPUType string

	for _, constraint := range policy.Constraints {
		switch constraint.Type {
		case "regulatory":
			eligible = filterRegulatory(eligible, constraint.Rule)
		case "hardware":
			eligible = filterHardware(eligible, constraint.Rule)
			parts := strings.SplitN(constraint.Rule, "=", 2)
			if len(parts) == 2 && strings.HasPrefix(constraint.Rule, "gpu-type=") {
				requestedGPUType = parts[1]
			}
		}
	}

	if len(eligible) == 0 {
		return nil, fmt.Errorf("no feasible placement: no clusters satisfy all constraints")
	}

	// Build placement decisions with affinity-based scores.
	decisions := make([]PlacementDecision, 0, len(eligible))
	for _, cluster := range eligible {
		score := scoreCluster(cluster, policy.Affinity)
		gpuType := requestedGPUType
		if gpuType == "" && len(cluster.GPUCapacity.Types) > 0 {
			gpuType = cluster.GPUCapacity.Types[0]
		}
		decisions = append(decisions, PlacementDecision{
			ClusterID: cluster.ID,
			Score:     score,
			GPUType:   gpuType,
			Replicas:  1,
			Reasons:   []string{"passed constraints"},
		})
	}

	// Sort by score descending.
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].Score > decisions[j].Score
	})

	// Limit to MaxClusters.
	maxClusters := pool.Placement.MaxClusters
	if maxClusters > 0 && len(decisions) > maxClusters {
		decisions = decisions[:maxClusters]
	}

	return decisions, nil
}

// filterRegulatory keeps clusters whose labels match the regulatory rule
// (format: "key=value").
func filterRegulatory(clusters []ClusterInfo, rule string) []ClusterInfo {
	parts := strings.SplitN(rule, "=", 2)
	if len(parts) != 2 {
		return clusters
	}
	key, value := parts[0], parts[1]
	var result []ClusterInfo
	for _, c := range clusters {
		if c.Labels[key] == value {
			result = append(result, c)
		}
	}
	return result
}

// filterHardware keeps clusters that have the requested GPU type available
// (format: "gpu-type=TYPE").
func filterHardware(clusters []ClusterInfo, rule string) []ClusterInfo {
	parts := strings.SplitN(rule, "=", 2)
	if len(parts) != 2 {
		return clusters
	}
	gpuType := parts[1]
	var result []ClusterInfo
	for _, c := range clusters {
		for _, t := range c.GPUCapacity.Types {
			if t == gpuType {
				result = append(result, c)
				break
			}
		}
	}
	return result
}

// scoreCluster computes a placement score for the cluster based on the
// provided affinity rules. Lower utilization yields a higher score.
func scoreCluster(cluster ClusterInfo, affinity []v1alpha1.AffinityRule) float64 {
	if len(affinity) == 0 {
		// Default scoring: prefer lower utilization.
		return 1 - cluster.Utilization
	}
	score := 0.0
	totalWeight := 0.0
	for _, rule := range affinity {
		switch rule.Type {
		case "cost-optimization":
			score += rule.Weight * (1 - cluster.Utilization)
			totalWeight += rule.Weight
		}
	}
	if totalWeight > 0 {
		return score / totalWeight
	}
	return 1 - cluster.Utilization
}

// ModelPackAwareConstraintSolver wraps an existing ConstraintSolver and
// enriches placement decisions with GPU requirement information derived from
// ModelPack metadata. When a pool's ModelSpec includes an OciRef, the solver
// resolves the ModelPack config and uses ComputeGPURequirements to auto-derive
// hardware constraints (GPU type, count, tensor parallelism) before delegating
// to the inner solver.
type ModelPackAwareConstraintSolver struct {
	inner    ConstraintSolver
	resolver modelpack.ModelResolver
}

// NewModelPackAwareConstraintSolver creates a solver that enriches placement
// with ModelPack-derived GPU requirements.
func NewModelPackAwareConstraintSolver(inner ConstraintSolver, resolver modelpack.ModelResolver) *ModelPackAwareConstraintSolver {
	return &ModelPackAwareConstraintSolver{
		inner:    inner,
		resolver: resolver,
	}
}

// Solve resolves ModelPack metadata (when an OciRef is present) and injects
// GPU hardware constraints into the placement policy before delegating to the
// wrapped solver.
func (s *ModelPackAwareConstraintSolver) Solve(ctx context.Context, pool v1alpha1.FleetInferencePoolSpec, clusters []ClusterInfo, policy v1alpha1.PlacementPolicySpec) ([]PlacementDecision, error) {
	enrichedPolicy := policy

	if pool.Model.OciRef != "" && s.resolver != nil {
		config, err := s.resolver.Resolve(ctx, pool.Model.OciRef)
		if err == nil && config != nil {
			gpuReqs, err := modelpack.ComputeGPURequirements(config)
			if err == nil && gpuReqs != nil {
				// Inject GPU type constraints derived from the model metadata
				// if the policy does not already specify hardware constraints.
				if !hasHardwareConstraint(enrichedPolicy) && len(gpuReqs.SupportedGPUTypes) > 0 {
					enrichedPolicy.Constraints = append(enrichedPolicy.Constraints, v1alpha1.PlacementConstraint{
						Type: "hardware",
						Rule: fmt.Sprintf("gpu-type=%s", gpuReqs.SupportedGPUTypes[0]),
					})
				}
			}
		}
		// If resolution fails, proceed without enrichment -- the inner solver
		// will use whatever constraints are already in the policy.
	}

	return s.inner.Solve(ctx, pool, clusters, enrichedPolicy)
}

// hasHardwareConstraint checks whether the policy already contains a hardware
// constraint, to avoid overriding explicit user configuration.
func hasHardwareConstraint(policy v1alpha1.PlacementPolicySpec) bool {
	for _, c := range policy.Constraints {
		if c.Type == "hardware" {
			return true
		}
	}
	return false
}
