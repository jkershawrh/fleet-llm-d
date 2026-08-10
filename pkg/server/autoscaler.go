package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/actuator"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/optimizer"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
)

func (fc *FleetController) runAutoscalingLoop(ctx context.Context) {
	if fc.Actuator == nil {
		slog.Info("autoscaling loop disabled: no actuator configured")
		return
	}

	interval := 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("autoscaling loop started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			slog.Info("autoscaling loop stopped")
			return
		case <-ticker.C:
			fc.runAutoscalingCycle(ctx)
		}
	}
}

func (fc *FleetController) runAutoscalingCycle(ctx context.Context) {
	metrics, err := fc.MetricsCollector.CollectAll(ctx)
	if err != nil {
		slog.Warn("autoscaler: failed to collect metrics", "error", err)
		return
	}
	if len(metrics) == 0 {
		return
	}

	// Re-evaluate routing across clusters based on live health data.
	fc.updateRoutingFromHealth(ctx)

	pools := fc.Reconciler.ListPools()
	if len(pools) == 0 {
		return
	}

	for _, pool := range pools {
		scalingRef := pool.ScalingPolicyRef
		if scalingRef == "" {
			continue
		}

		policy := fc.resolveScalingPolicy(scalingRef)
		if policy == nil {
			continue
		}

		poolMetrics := metricsForPool(metrics, pool.Name, pool.DesiredClusters)
		if len(poolMetrics) == 0 {
			continue
		}
		actions, err := fc.Optimizer.Optimize(ctx, poolMetrics, *policy)
		if err != nil {
			slog.Warn("autoscaler: optimizer failed", "pool", pool.Name, "error", err)
			continue
		}

		action, ok := aggregateScalingActions(pool.Name, poolMetrics, actions)
		if !ok || action.DesiredReplicas == action.CurrentReplicas {
			continue
		}

		direction := "scale_up"
		if action.DesiredReplicas < action.CurrentReplicas {
			direction = "scale_down"
		}

		slog.Info("autoscaler: scaling",
			"pool", action.PoolName,
			"clusters", action.ClusterID,
			"direction", direction,
			"from", action.CurrentReplicas,
			"to", action.DesiredReplicas,
			"reason", action.Reason,
		)

		namespace := fc.Reconciler.Namespace()
		if err := fc.Actuator.ScaleDeployment(ctx, action.PoolName, namespace, action.DesiredReplicas); err != nil {
			slog.Warn("autoscaler: actuation failed",
				"pool", action.PoolName,
				"clusters", action.ClusterID,
				"error", err,
			)
			continue
		}

		RecordAutoscalerAction(direction)
		_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
			Type: events.EventModelScaled, Source: "urn:fleet-llm-d:autoscaler",
			Subject: action.PoolName, Timestamp: time.Now().UTC(),
			Payload: map[string]interface{}{
				"clusters": action.ClusterID, "from": action.CurrentReplicas,
				"to": action.DesiredReplicas, "direction": direction, "reason": action.Reason,
			},
		})

		if fc.FleetRecorder != nil {
			if _, recordErr := fc.FleetRecorder.RecordScalingEvent(
				ctx,
				action.ClusterID,
				action.PoolName,
				action.CurrentReplicas,
				action.DesiredReplicas,
				action.Reason,
			); recordErr != nil {
				slog.Warn("autoscaler: ledger record failed", "error", recordErr)
			}
		}
	}
}

func metricsForPool(metrics []collector.ClusterMetrics, poolName string, desiredClusters []string) []collector.ClusterMetrics {
	allowed := make(map[string]struct{}, len(desiredClusters))
	for _, clusterID := range desiredClusters {
		allowed[clusterID] = struct{}{}
	}
	result := make([]collector.ClusterMetrics, 0, len(metrics))
	for _, cluster := range metrics {
		if len(allowed) > 0 {
			if _, ok := allowed[cluster.ClusterID]; !ok {
				continue
			}
		}
		filtered := collector.ClusterMetrics{ClusterID: cluster.ClusterID, Timestamp: cluster.Timestamp}
		for _, pool := range cluster.Pools {
			if pool.PoolName == poolName {
				filtered.Pools = append(filtered.Pools, pool)
			}
		}
		if len(filtered.Pools) > 0 {
			result = append(result, filtered)
		}
	}
	return result
}

func aggregateScalingActions(poolName string, metrics []collector.ClusterMetrics, actions []optimizer.ScalingAction) (optimizer.ScalingAction, bool) {
	if len(actions) == 0 {
		return optimizer.ScalingAction{}, false
	}
	currentTotal := 0
	for _, cluster := range metrics {
		for _, pool := range cluster.Pools {
			currentTotal += pool.Replicas
		}
	}
	desiredTotal := currentTotal
	clusters := make([]string, 0, len(actions))
	reasons := make([]string, 0, len(actions))
	for _, action := range actions {
		desiredTotal += action.DesiredReplicas - action.CurrentReplicas
		clusters = append(clusters, action.ClusterID)
		if !containsString(reasons, action.Reason) {
			reasons = append(reasons, action.Reason)
		}
	}
	sort.Strings(clusters)
	return optimizer.ScalingAction{
		ClusterID: strings.Join(clusters, ","), PoolName: poolName,
		CurrentReplicas: currentTotal, DesiredReplicas: desiredTotal,
		Reason: strings.Join(reasons, "; "),
	}, true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (fc *FleetController) resolveScalingPolicy(name string) *v1alpha1.FleetScalingPolicySpec {
	if fc.CRDWatcher == nil {
		return defaultScalingPolicy()
	}

	policy, err := fc.CRDWatcher.GetScalingPolicy(name)
	if err != nil {
		slog.Debug("autoscaler: scaling policy not found, using default", "name", name, "error", err)
		return defaultScalingPolicy()
	}
	return policy
}

func defaultScalingPolicy() *v1alpha1.FleetScalingPolicySpec {
	return &v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "queueDepth", Target: "5"},
			{Metric: "gpuUtilization", Target: "0.8"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			MaxScaleUpRate: 2,
		},
		Strategy: "reactive",
	}
}

// updateRoutingFromHealth re-evaluates cross-cluster routing using live
// cluster health metrics and pushes updated weights to Praxis via the
// overlay adapter. This is the feedback loop: agent metrics → cluster
// health → routing evaluation → Praxis config → inference routing.
func (fc *FleetController) updateRoutingFromHealth(ctx context.Context) {
	if fc.PraxisOverlay == nil {
		return
	}

	health := fc.BuildClusterHealth(ctx)
	if len(health) == 0 {
		return
	}

	// Include all routing inputs so changes in load or available capacity are
	// not hidden merely because the healthy cluster IDs stayed constant.
	fingerprint := routingHealthFingerprint(health)
	if fingerprint == fc.lastRoutingFingerprint {
		return
	}

	pools := fc.Reconciler.ListPools()
	if len(pools) == 0 {
		return
	}

	var placements []routing.PoolPlacement
	for _, pool := range pools {
		if len(pool.DesiredClusters) == 0 {
			continue
		}

		// Keep only healthy, non-saturated destinations and order them by a
		// capacity/cache/latency score. An empty result deliberately removes the
		// route instead of failing open to unhealthy destinations.
		var candidates []policy.ClusterHealth
		for _, cid := range pool.DesiredClusters {
			for _, ch := range health {
				if ch.ClusterID == cid && routableCluster(ch) {
					candidates = append(candidates, ch)
					break
				}
			}
		}
		if len(candidates) == 0 {
			slog.Warn("routing: no healthy destination; removing pool route", "pool", pool.Name)
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			return routingHealthScore(candidates[i]) > routingHealthScore(candidates[j])
		})
		orderedClusters := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			orderedClusters = append(orderedClusters, candidate.ClusterID)
		}

		placements = append(placements, routing.PoolPlacement{
			ModelName: pool.Model,
			Clusters:  orderedClusters,
		})
	}

	cfg, err := fc.PraxisOverlay.RenderConfig(placements)
	if err != nil {
		slog.Warn("routing: failed to render Praxis overlay", "error", err)
		return
	}

	ns := fc.Reconciler.Namespace()
	if ns == "" {
		ns = "fleet-llm-d"
	}
	if err := writePraxisConfigMap(fc.KubeAPI, ns, cfg); err != nil {
		slog.Debug("routing: failed to update Praxis config", "error", err)
		return
	}
	fc.lastRoutingFingerprint = fingerprint

	healthyCount := 0
	for _, cluster := range health {
		if routableCluster(cluster) {
			healthyCount++
		}
	}
	slog.Info("routing: Praxis config updated from cluster health",
		"healthy_clusters", healthyCount,
		"placements", len(placements),
	)

	_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
		Type: events.EventRoutingUpdated, Source: "urn:fleet-llm-d:autoscaler",
		Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{
			"healthy_clusters": healthyCount,
			"placements":       len(placements),
		},
	})
}

func routableCluster(cluster policy.ClusterHealth) bool {
	if !cluster.Healthy {
		return false
	}
	return cluster.PoolSaturation < 1 && cluster.CurrentLoad < 1
}

func routingHealthScore(cluster policy.ClusterHealth) float64 {
	latencyPenalty := cluster.LatencyMs / 1000
	return cluster.CapacityRemaining + cluster.KVCacheHitRate - cluster.CurrentLoad - cluster.PoolSaturation - latencyPenalty
}

func routingHealthFingerprint(health []policy.ClusterHealth) string {
	snapshot := append([]policy.ClusterHealth(nil), health...)
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].ClusterID < snapshot[j].ClusterID })
	encoded, _ := json.Marshal(snapshot)
	return string(encoded)
}

// Actuator is set on the FleetController when autoscaling actuation is enabled.
func NewActuator(apiServer, token string) *actuator.ModelPlaneActuator {
	return actuator.NewModelPlaneActuator(apiServer, token)
}
