//go:build bdd

package steps

import (
	"fmt"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

// SetScalingPolicy creates a scaling policy in the world.
func (w *World) SetScalingPolicy(policyName, strategy string) {
	if _, ok := w.FleetPools[policyName]; !ok {
		w.FleetPools[policyName] = &PoolState{}
	}
	w.FleetPools[policyName].ScalingPolicy = v1alpha1.FleetScalingPolicySpec{
		Strategy: strategy,
	}
}

// AddScalingObjective adds a scaling objective to a policy.
func (w *World) AddScalingObjective(policyName, metric, target string) {
	if pool, ok := w.FleetPools[policyName]; ok {
		pool.ScalingPolicy.Objectives = append(pool.ScalingPolicy.Objectives, v1alpha1.ScalingObjective{
			Metric: metric,
			Target: target,
		})
	}
}

// SetScalingConstraints sets the scaling constraints on a policy.
func (w *World) SetScalingConstraints(policyName string, globalMaxGPUs, maxScaleUpRate int) {
	if pool, ok := w.FleetPools[policyName]; ok {
		pool.ScalingPolicy.Constraints = v1alpha1.ScalingConstraints{
			GlobalMaxGPUs:  globalMaxGPUs,
			MaxScaleUpRate: maxScaleUpRate,
		}
	}
}

// SetCrossClusterMigration enables cross-cluster migration on a policy.
func (w *World) SetCrossClusterMigration(policyName string, enabled bool, threshold float64) {
	if pool, ok := w.FleetPools[policyName]; ok {
		pool.ScalingPolicy.CrossCluster = &v1alpha1.CrossClusterScaling{
			EnableMigration:    enabled,
			MigrationThreshold: threshold,
		}
	}
}

// SetClusterMetrics registers metrics for a cluster in the collector.
func (w *World) SetClusterMetrics(clusterID, poolName, model string, replicas int,
	ttftP99 float64, gpuUtil float64, throughput float64, kvCacheHitRate float64) {
	metrics := collector.ClusterMetrics{
		ClusterID: clusterID,
		Pools: []collector.PoolMetrics{
			{
				PoolName:       poolName,
				Model:          model,
				Replicas:       replicas,
				QueueDepth:     0,
				TTFT_P99_Ms:    ttftP99,
				Throughput_TPS: throughput,
				GPUUtilization: gpuUtil,
				KVCacheHitRate: kvCacheHitRate,
			},
		},
		Timestamp: time.Now(),
	}
	w.Collector.Add(metrics)

	// Also update cluster state
	if cs, ok := w.Clusters[clusterID]; ok {
		cs.TTFT_P99_Ms = ttftP99
		cs.GPUUtilization = gpuUtil
		cs.Throughput = throughput
		cs.KVCacheHitRate = kvCacheHitRate
		cs.PoolMetrics = metrics.Pools[0]
	}
}

// SetClusterReplicas sets the current replica count for a cluster.
func (w *World) SetClusterReplicas(clusterID string, replicas, gpusPerReplica int) {
	if cs, ok := w.Clusters[clusterID]; ok {
		cs.Replicas = replicas
		cs.GPUsPerReplica = gpusPerReplica
	}
}

// RunOptimizer runs the fleet optimizer with the given policy.
func (w *World) RunOptimizer(policyName string) error {
	policyPool, ok := w.FleetPools[policyName]
	if !ok {
		return fmt.Errorf("scaling policy %q not found", policyName)
	}

	metrics, err := w.Collector.CollectAll(w.Ctx)
	if err != nil {
		w.LastError = err
		return nil
	}

	actions, err := w.Optimizer.Optimize(w.Ctx, metrics, policyPool.ScalingPolicy)
	if err != nil {
		w.LastError = err
		return nil
	}

	w.LastScaling = &ScalingResult{Actions: actions}
	w.LastError = nil
	return nil
}

// AssertScaleAction checks that a scaling action was proposed for a cluster.
func (w *World) AssertScaleAction(clusterID string, direction string) error {
	if w.LastScaling == nil {
		return fmt.Errorf("no scaling result available")
	}

	for _, action := range w.LastScaling.Actions {
		if action.ClusterID == clusterID {
			switch direction {
			case "up":
				if action.DesiredReplicas > action.CurrentReplicas {
					return nil
				}
				return fmt.Errorf("expected scale up for %q, but desired %d <= current %d",
					clusterID, action.DesiredReplicas, action.CurrentReplicas)
			case "down":
				if action.DesiredReplicas < action.CurrentReplicas {
					return nil
				}
				return fmt.Errorf("expected scale down for %q, but desired %d >= current %d",
					clusterID, action.DesiredReplicas, action.CurrentReplicas)
			}
		}
	}
	return fmt.Errorf("no scaling action found for cluster %q", clusterID)
}

// AssertDesiredReplicas checks the desired replica count for a cluster.
func (w *World) AssertDesiredReplicas(clusterID string, minReplicas int) error {
	if w.LastScaling == nil {
		return fmt.Errorf("no scaling result available")
	}

	for _, action := range w.LastScaling.Actions {
		if action.ClusterID == clusterID {
			if action.DesiredReplicas >= minReplicas {
				return nil
			}
			return fmt.Errorf("cluster %q desired replicas %d, expected at least %d",
				clusterID, action.DesiredReplicas, minReplicas)
		}
	}
	return fmt.Errorf("no scaling action found for cluster %q", clusterID)
}

// AssertTotalGPUsNotExceed checks that total GPU consumption does not exceed a limit.
func (w *World) AssertTotalGPUsNotExceed(maxGPUs int) error {
	if w.LastScaling == nil {
		return nil
	}

	totalGPUs := 0
	for _, action := range w.LastScaling.Actions {
		// Assume 4 GPUs per replica as a default
		gpusPerReplica := 4
		if cs, ok := w.Clusters[action.ClusterID]; ok && cs.GPUsPerReplica > 0 {
			gpusPerReplica = cs.GPUsPerReplica
		}
		totalGPUs += action.DesiredReplicas * gpusPerReplica
	}

	if totalGPUs > maxGPUs {
		return fmt.Errorf("total GPUs %d exceeds max %d", totalGPUs, maxGPUs)
	}
	return nil
}

// AssertMigrationProposed checks that a cross-cluster migration was proposed.
func (w *World) AssertMigrationProposed(fromCluster, toCluster string) error {
	if w.LastScaling == nil {
		return fmt.Errorf("no scaling result available")
	}

	hasScaleDown := false
	hasScaleUp := false
	for _, action := range w.LastScaling.Actions {
		if action.ClusterID == fromCluster && action.DesiredReplicas < action.CurrentReplicas {
			hasScaleDown = true
		}
		if action.ClusterID == toCluster && action.DesiredReplicas > action.CurrentReplicas {
			hasScaleUp = true
		}
	}

	if !hasScaleDown && !hasScaleUp {
		return fmt.Errorf("no migration from %q to %q found in scaling actions", fromCluster, toCluster)
	}
	return nil
}

// AssertMinReplicaRespected checks no cluster is scaled below a minimum.
func (w *World) AssertMinReplicaRespected(minReplicas int) error {
	if w.LastScaling == nil {
		return nil
	}
	for _, action := range w.LastScaling.Actions {
		if action.DesiredReplicas < minReplicas {
			return fmt.Errorf("cluster %q scaled to %d replicas, below minimum %d",
				action.ClusterID, action.DesiredReplicas, minReplicas)
		}
	}
	return nil
}
