package server

import (
	"context"
	"log/slog"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/actuator"
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

// Actuator is set on the FleetController when autoscaling actuation is enabled.
func NewActuator(apiServer, token string) *actuator.ModelPlaneActuator {
	return actuator.NewModelPlaneActuator(apiServer, token)
}
