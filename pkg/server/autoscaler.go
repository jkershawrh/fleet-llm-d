package server

import (
	"context"
	"log/slog"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/actuator"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
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

		actions, err := fc.Optimizer.Optimize(ctx, metrics, *policy)
		if err != nil {
			slog.Warn("autoscaler: optimizer failed", "pool", pool.Name, "error", err)
			continue
		}

		for _, action := range actions {
			if action.DesiredReplicas == action.CurrentReplicas {
				continue
			}

			direction := "scale_up"
			if action.DesiredReplicas < action.CurrentReplicas {
				direction = "scale_down"
			}

			slog.Info("autoscaler: scaling",
				"pool", action.PoolName,
				"cluster", action.ClusterID,
				"direction", direction,
				"from", action.CurrentReplicas,
				"to", action.DesiredReplicas,
				"reason", action.Reason,
			)

			namespace := fc.Reconciler.Namespace()
			if err := fc.Actuator.ScaleDeployment(ctx, action.PoolName, namespace, action.DesiredReplicas); err != nil {
				slog.Warn("autoscaler: actuation failed",
					"pool", action.PoolName,
					"cluster", action.ClusterID,
					"error", err,
				)
				continue
			}

			RecordAutoscalerAction(direction)
			_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
				Type: events.EventModelScaled, Source: "urn:fleet-llm-d:autoscaler",
				Subject: action.PoolName, Timestamp: time.Now().UTC(),
				Payload: map[string]interface{}{
					"cluster": action.ClusterID, "from": action.CurrentReplicas,
					"to": action.DesiredReplicas, "direction": direction, "reason": action.Reason,
				},
			})

			if fc.FleetRecorder != nil {
				if _, recordErr := fc.FleetRecorder.RecordScalingEvent(
					ctx,
					action.PoolName,
					action.ClusterID,
					action.CurrentReplicas,
					action.DesiredReplicas,
					action.Reason,
				); recordErr != nil {
					slog.Warn("autoscaler: ledger record failed", "error", recordErr)
				}
			}
		}
	}
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

	// Only update if the healthy cluster set changed since last evaluation.
	healthySet := make(map[string]bool)
	for _, ch := range health {
		if ch.Healthy {
			healthySet[ch.ClusterID] = true
		}
	}
	if fc.lastHealthySet != nil && sameKeys(fc.lastHealthySet, healthySet) {
		return
	}
	fc.lastHealthySet = healthySet

	pools := fc.Reconciler.ListPools()
	if len(pools) == 0 {
		return
	}

	var placements []routing.PoolPlacement
	for _, pool := range pools {
		if len(pool.DesiredClusters) == 0 {
			continue
		}

		// Build weighted cluster list from health scores.
		var weightedClusters []string
		for _, cid := range pool.DesiredClusters {
			for _, ch := range health {
				if ch.ClusterID == cid && ch.Healthy {
					weightedClusters = append(weightedClusters, cid)
					break
				}
			}
		}
		if len(weightedClusters) == 0 {
			weightedClusters = pool.DesiredClusters
		}

		placements = append(placements, routing.PoolPlacement{
			ModelName: pool.Model,
			Clusters:  weightedClusters,
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
	if err := writePraxisConfigMap("https://kubernetes.default.svc", ns, cfg); err != nil {
		slog.Debug("routing: failed to update Praxis config", "error", err)
		return
	}

	slog.Info("routing: Praxis config updated from cluster health",
		"healthy_clusters", len(health),
		"placements", len(placements),
	)

	_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
		Type: events.EventRoutingUpdated, Source: "urn:fleet-llm-d:autoscaler",
		Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{
			"healthy_clusters": len(health),
			"placements":       len(placements),
		},
	})
}

func sameKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// Actuator is set on the FleetController when autoscaling actuation is enabled.
func NewActuator(apiServer, token string) *actuator.ModelPlaneActuator {
	return actuator.NewModelPlaneActuator(apiServer, token)
}
