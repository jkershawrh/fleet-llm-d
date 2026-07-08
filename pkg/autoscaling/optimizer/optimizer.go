package optimizer

import (
	"context"
	"fmt"
	"math"
	"strconv"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

// ScalingAction represents a single scaling decision produced by the optimizer.
type ScalingAction struct {
	ClusterID       string
	PoolName        string
	DesiredReplicas int
	CurrentReplicas int
	Reason          string
}

// FleetOptimizer computes scaling actions from cluster metrics and a scaling policy.
type FleetOptimizer interface {
	Optimize(ctx context.Context, metrics []collector.ClusterMetrics, policy v1alpha1.FleetScalingPolicySpec) ([]ScalingAction, error)
}

type defaultFleetOptimizer struct{}

// NewFleetOptimizer returns a new FleetOptimizer.
func NewFleetOptimizer() FleetOptimizer {
	return &defaultFleetOptimizer{}
}

// scalingObjective is an internal representation of a parsed objective.
type scalingObjective struct {
	metric string
	target float64
}

// poolState tracks per-pool evaluation results across the optimisation pass.
type poolState struct {
	clusterID     string
	pool          collector.PoolMetrics
	overloaded    bool
	underutilized bool
}

func (o *defaultFleetOptimizer) Optimize(
	ctx context.Context,
	metrics []collector.ClusterMetrics,
	pol v1alpha1.FleetScalingPolicySpec,
) ([]ScalingAction, error) {

	// --- 1. Parse objectives --------------------------------------------------
	objectives, err := parseObjectives(pol.Objectives)
	if err != nil {
		return nil, err
	}

	var actions []ScalingAction
	var states []poolState

	// --- 2. Evaluate each pool against objectives -----------------------------
	for _, cluster := range metrics {
		for _, pool := range cluster.Pools {
			needsScaleUp := false
			needsScaleDown := false
			maxRatio := 1.0

			for _, obj := range objectives {
				actual := metricValue(pool, obj.metric)
				switch obj.metric {
				case "queueDepth", "ttft_p99_ms", "inferenceLatencyP99Ms":
					if actual > obj.target {
						needsScaleUp = true
						ratio := actual / obj.target
						if ratio > maxRatio {
							maxRatio = ratio
						}
					}
				case "gpuUtilization", "cpuUtilization":
					// Scale-down: use cpuUtilization for CPU pools, gpuUtilization for GPU pools.
					// For cpuUtilization objectives, check whether actual CPU use is below target.
					// For gpuUtilization objectives on CPU-only pools (GPU=0, CPU>0), use CPU utilization instead.
					effectiveActual := actual
					if obj.metric == "gpuUtilization" && pool.GPUUtilization == 0 && pool.CPUUtilization > 0 {
						effectiveActual = pool.CPUUtilization
					}
					if effectiveActual < obj.target {
						needsScaleDown = true
					}
				}
			}

			st := poolState{
				clusterID:     cluster.ClusterID,
				pool:          pool,
				overloaded:    needsScaleUp,
				underutilized: needsScaleDown && !needsScaleUp,
			}
			states = append(states, st)

			if needsScaleUp {
				additional := int(math.Ceil(float64(pool.Replicas) * (maxRatio - 1)))
				if additional > pol.Constraints.MaxScaleUpRate {
					additional = pol.Constraints.MaxScaleUpRate
				}
				if additional < 1 {
					additional = 1
				}
				actions = append(actions, ScalingAction{
					ClusterID:       cluster.ClusterID,
					PoolName:        pool.PoolName,
					DesiredReplicas: pool.Replicas + additional,
					CurrentReplicas: pool.Replicas,
					Reason:          "metrics exceed target thresholds",
				})
			} else if needsScaleDown && (pol.CrossCluster == nil || !pol.CrossCluster.EnableMigration) {
				// Direct scale-down (no migration consideration).
				desired := scaleDownReplicas(pool, objectives, pol.ScaleToZero)
				actions = append(actions, ScalingAction{
					ClusterID:       cluster.ClusterID,
					PoolName:        pool.PoolName,
					DesiredReplicas: desired,
					CurrentReplicas: pool.Replicas,
					Reason:          "underutilized: GPU utilization below target",
				})
			}
		}
	}

	// --- 3. Cross-cluster migration -------------------------------------------
	if pol.CrossCluster != nil && pol.CrossCluster.EnableMigration {
		for _, st := range states {
			if !st.underutilized {
				continue
			}
			// Look for an overloaded pool with the same name in another cluster.
			shouldAbsorb := false
			for _, other := range states {
				if other.overloaded &&
					other.pool.PoolName == st.pool.PoolName &&
					other.clusterID != st.clusterID {
					// Use cpuUtilization for CPU pools (GPUUtilization == 0), gpuUtilization otherwise.
					otherUtil := other.pool.GPUUtilization
					if otherUtil == 0 && other.pool.CPUUtilization > 0 {
						otherUtil = other.pool.CPUUtilization
					}
					stUtil := st.pool.GPUUtilization
					if stUtil == 0 && st.pool.CPUUtilization > 0 {
						stUtil = st.pool.CPUUtilization
					}
					diff := otherUtil - stUtil
					if diff >= pol.CrossCluster.MigrationThreshold {
						shouldAbsorb = true
						break
					}
				}
			}
			if shouldAbsorb {
				additional := 1
				if additional > pol.Constraints.MaxScaleUpRate {
					additional = pol.Constraints.MaxScaleUpRate
				}
				actions = append(actions, ScalingAction{
					ClusterID:       st.clusterID,
					PoolName:        st.pool.PoolName,
					DesiredReplicas: st.pool.Replicas + additional,
					CurrentReplicas: st.pool.Replicas,
					Reason:          "cross-cluster migration: absorbing load from overloaded cluster",
				})
			} else {
				desired := scaleDownReplicas(st.pool, objectives, pol.ScaleToZero)
				actions = append(actions, ScalingAction{
					ClusterID:       st.clusterID,
					PoolName:        st.pool.PoolName,
					DesiredReplicas: desired,
					CurrentReplicas: st.pool.Replicas,
					Reason:          "underutilized: GPU utilization below target",
				})
			}
		}
	}

	// --- 4. Enforce global GPU constraint -------------------------------------
	if pol.Constraints.GlobalMaxGPUs > 0 {
		totalDesired := 0
		for _, a := range actions {
			totalDesired += a.DesiredReplicas
		}
		if totalDesired > pol.Constraints.GlobalMaxGPUs {
			factor := float64(pol.Constraints.GlobalMaxGPUs) / float64(totalDesired)
			for i := range actions {
				scaled := int(math.Floor(float64(actions[i].DesiredReplicas) * factor))
				if scaled < 1 {
					scaled = 1
				}
				actions[i].DesiredReplicas = scaled
			}
			// After floor-rounding, distribute any remaining budget.
			newTotal := 0
			for _, a := range actions {
				newTotal += a.DesiredReplicas
			}
			for i := range actions {
				if newTotal >= pol.Constraints.GlobalMaxGPUs {
					break
				}
				actions[i].DesiredReplicas++
				newTotal++
			}
		}
	}

	return actions, nil
}

// parseObjectives converts API objectives into internal representation.
func parseObjectives(specs []v1alpha1.ScalingObjective) ([]scalingObjective, error) {
	out := make([]scalingObjective, 0, len(specs))
	for _, s := range specs {
		t, err := strconv.ParseFloat(s.Target, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid target %q for metric %q: %w", s.Target, s.Metric, err)
		}
		out = append(out, scalingObjective{metric: s.Metric, target: t})
	}
	return out, nil
}

// metricValue extracts the named metric from a PoolMetrics struct.
func metricValue(p collector.PoolMetrics, metric string) float64 {
	switch metric {
	case "queueDepth":
		return float64(p.QueueDepth)
	case "ttft_p99_ms":
		return p.TTFT_P99_Ms
	case "gpuUtilization":
		return p.GPUUtilization
	case "throughput_tps":
		return p.Throughput_TPS
	case "kvCacheHitRate":
		return p.KVCacheHitRate
	case "cpuUtilization":
		return p.CPUUtilization
	case "inferenceLatencyP99Ms":
		return p.InferenceLatencyP99Ms
	default:
		return 0
	}
}

// objectiveTarget returns the target value for the named metric, or
// a safe default of 1.0 if no such objective exists.
func objectiveTarget(objectives []scalingObjective, metric string) float64 {
	for _, o := range objectives {
		if o.metric == metric {
			return o.target
		}
	}
	return 1.0
}

// scaleDownReplicas computes the desired replica count when a pool is
// underutilized, honouring ScaleToZero if enabled. For CPU-only pools
// (GPUUtilization == 0, CPUUtilization > 0) it uses CPU utilization
// instead of GPU utilization.
func scaleDownReplicas(pool collector.PoolMetrics, objectives []scalingObjective, stz *v1alpha1.ScaleToZeroSpec) int {
	minReplicas := 1
	if stz != nil && stz.Enabled {
		minReplicas = 0
	}

	// Use cpuUtilization for CPU pools, gpuUtilization for GPU pools.
	utilization := pool.GPUUtilization
	targetMetric := "gpuUtilization"
	if pool.GPUUtilization == 0 && pool.CPUUtilization > 0 {
		utilization = pool.CPUUtilization
		targetMetric = "cpuUtilization"
	}

	target := objectiveTarget(objectives, targetMetric)
	if target <= 0 {
		target = 1.0
	}

	desired := int(math.Floor(float64(pool.Replicas) * utilization / target))
	if desired >= pool.Replicas {
		desired = pool.Replicas - 1
	}
	if desired < minReplicas {
		desired = minReplicas
	}
	return desired
}
