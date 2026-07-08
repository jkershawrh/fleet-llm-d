package optimizer

import (
	"context"
	"testing"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

func TestOptimize(t *testing.T) {
	tests := []struct {
		name    string
		metrics []collector.ClusterMetrics
		policy  v1alpha1.FleetScalingPolicySpec
		check   func(t *testing.T, actions []ScalingAction, err error)
	}{
		{
			name: "ScaleUp",
			metrics: []collector.ClusterMetrics{
				{
					ClusterID: "us-east-1",
					Pools: []collector.PoolMetrics{
						{
							PoolName:       "llama-70b",
							Model:          "meta-llama/Llama-3-70B",
							Replicas:       2,
							QueueDepth:     150,
							TTFT_P99_Ms:    850.0,
							Throughput_TPS: 12.5,
							GPUUtilization: 0.92,
							KVCacheHitRate: 0.65,
						},
					},
					Timestamp: time.Now(),
				},
			},
			policy: v1alpha1.FleetScalingPolicySpec{
				Objectives: []v1alpha1.ScalingObjective{
					{Metric: "queueDepth", Target: "50"},
					{Metric: "ttft_p99_ms", Target: "500"},
				},
				Constraints: v1alpha1.ScalingConstraints{
					GlobalMaxGPUs:  64,
					MaxScaleUpRate: 4,
				},
				Strategy: "reactive",
			},
			check: func(t *testing.T, actions []ScalingAction, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(actions) == 0 {
					t.Fatal("expected at least one scaling action")
				}
				a := actions[0]
				if a.DesiredReplicas <= a.CurrentReplicas {
					t.Errorf("expected scale-up: DesiredReplicas (%d) should be > CurrentReplicas (%d)", a.DesiredReplicas, a.CurrentReplicas)
				}
			},
		},
		{
			name: "ScaleDown",
			metrics: []collector.ClusterMetrics{
				{
					ClusterID: "eu-west-1",
					Pools: []collector.PoolMetrics{
						{
							PoolName:       "mistral-7b",
							Model:          "mistralai/Mistral-7B-v0.3",
							Replicas:       8,
							QueueDepth:     2,
							TTFT_P99_Ms:    45.0,
							Throughput_TPS: 1.2,
							GPUUtilization: 0.08,
							KVCacheHitRate: 0.90,
						},
					},
					Timestamp: time.Now(),
				},
			},
			policy: v1alpha1.FleetScalingPolicySpec{
				Objectives: []v1alpha1.ScalingObjective{
					{Metric: "gpuUtilization", Target: "0.60"},
				},
				Constraints: v1alpha1.ScalingConstraints{
					GlobalMaxGPUs:  64,
					MaxScaleUpRate: 4,
				},
				Strategy: "predictive",
				ScaleToZero: &v1alpha1.ScaleToZeroSpec{
					Enabled:        true,
					CooldownPeriod: "10m",
				},
			},
			check: func(t *testing.T, actions []ScalingAction, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(actions) == 0 {
					t.Fatal("expected at least one scaling action")
				}
				a := actions[0]
				if a.DesiredReplicas >= a.CurrentReplicas {
					t.Errorf("expected scale-down: DesiredReplicas (%d) should be < CurrentReplicas (%d)", a.DesiredReplicas, a.CurrentReplicas)
				}
			},
		},
		{
			name: "CrossClusterMigration",
			metrics: []collector.ClusterMetrics{
				{
					ClusterID: "us-east-1",
					Pools: []collector.PoolMetrics{
						{
							PoolName:       "llama-70b",
							Model:          "meta-llama/Llama-3-70B",
							Replicas:       6,
							QueueDepth:     200,
							TTFT_P99_Ms:    1200.0,
							Throughput_TPS: 40.0,
							GPUUtilization: 0.97,
							KVCacheHitRate: 0.55,
						},
					},
					Timestamp: time.Now(),
				},
				{
					ClusterID: "us-west-2",
					Pools: []collector.PoolMetrics{
						{
							PoolName:       "llama-70b",
							Model:          "meta-llama/Llama-3-70B",
							Replicas:       3,
							QueueDepth:     10,
							TTFT_P99_Ms:    120.0,
							Throughput_TPS: 8.0,
							GPUUtilization: 0.25,
							KVCacheHitRate: 0.80,
						},
					},
					Timestamp: time.Now(),
				},
			},
			policy: v1alpha1.FleetScalingPolicySpec{
				Objectives: []v1alpha1.ScalingObjective{
					{Metric: "queueDepth", Target: "50"},
					{Metric: "gpuUtilization", Target: "0.70"},
				},
				Constraints: v1alpha1.ScalingConstraints{
					GlobalMaxGPUs:  48,
					MaxScaleUpRate: 4,
				},
				Strategy: "balanced",
				CrossCluster: &v1alpha1.CrossClusterScaling{
					EnableMigration:    true,
					MigrationThreshold: 0.30,
				},
			},
			check: func(t *testing.T, actions []ScalingAction, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(actions) < 2 {
					t.Fatalf("expected actions for both clusters, got %d", len(actions))
				}
				// The overloaded cluster should scale up and the idle cluster
				// should absorb load (or the optimizer should produce a
				// migration action), so at least one action should target each
				// cluster.
				clusters := map[string]bool{}
				for _, a := range actions {
					clusters[a.ClusterID] = true
				}
				if !clusters["us-east-1"] || !clusters["us-west-2"] {
					t.Errorf("expected actions spanning both clusters, got clusters: %v", clusters)
				}
			},
		},
		{
			name: "GlobalGPUConstraint",
			metrics: []collector.ClusterMetrics{
				{
					ClusterID: "us-east-1",
					Pools: []collector.PoolMetrics{
						{
							PoolName:       "llama-70b",
							Model:          "meta-llama/Llama-3-70B",
							Replicas:       4,
							QueueDepth:     300,
							TTFT_P99_Ms:    1500.0,
							Throughput_TPS: 55.0,
							GPUUtilization: 0.95,
							KVCacheHitRate: 0.50,
						},
					},
					Timestamp: time.Now(),
				},
				{
					ClusterID: "eu-west-1",
					Pools: []collector.PoolMetrics{
						{
							PoolName:       "llama-70b",
							Model:          "meta-llama/Llama-3-70B",
							Replicas:       4,
							QueueDepth:     280,
							TTFT_P99_Ms:    1400.0,
							Throughput_TPS: 50.0,
							GPUUtilization: 0.93,
							KVCacheHitRate: 0.52,
						},
					},
					Timestamp: time.Now(),
				},
			},
			policy: v1alpha1.FleetScalingPolicySpec{
				Objectives: []v1alpha1.ScalingObjective{
					{Metric: "queueDepth", Target: "50"},
				},
				Constraints: v1alpha1.ScalingConstraints{
					GlobalMaxGPUs:  10, // tight budget: only 10 GPUs fleet-wide
					MaxScaleUpRate: 4,
				},
				Strategy: "reactive",
			},
			check: func(t *testing.T, actions []ScalingAction, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(actions) == 0 {
					t.Fatal("expected scaling actions even under GPU constraint")
				}
				totalDesired := 0
				for _, a := range actions {
					totalDesired += a.DesiredReplicas
				}
				globalMax := 10
				if totalDesired > globalMax {
					t.Errorf("total desired replicas (%d) exceeds global GPU limit (%d)", totalDesired, globalMax)
				}
			},
		},
	}

	opt := NewFleetOptimizer()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actions, err := opt.Optimize(context.Background(), tc.metrics, tc.policy)
			tc.check(t, actions, err)
		})
	}
}

func TestOptimizer_ScalesUpOnInferenceLatency(t *testing.T) {
	opt := NewFleetOptimizer()
	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "inferenceLatencyP99Ms", Target: "500"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			GlobalMaxGPUs:  64,
			MaxScaleUpRate: 4,
		},
	}
	metrics := []collector.ClusterMetrics{
		{
			ClusterID: "cpu-cluster",
			Pools: []collector.PoolMetrics{
				{
					PoolName:              "cpu-pool",
					Replicas:              2,
					InferenceLatencyP99Ms: 1200, // way over 500 target
					CPUUtilization:        0.8,
				},
			},
			Timestamp: time.Now(),
		},
	}
	actions, err := opt.Optimize(context.Background(), metrics, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scaleUp := false
	for _, a := range actions {
		if a.DesiredReplicas > a.CurrentReplicas {
			scaleUp = true
		}
	}
	if !scaleUp {
		t.Error("should recommend scale-up when inference latency exceeds target")
	}
}

func TestOptimizer_DoesNotScaleDownActiveCPUPool(t *testing.T) {
	opt := NewFleetOptimizer()
	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "cpuUtilization", Target: "0.30"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			GlobalMaxGPUs:  64,
			MaxScaleUpRate: 4,
		},
	}
	metrics := []collector.ClusterMetrics{
		{
			ClusterID: "cpu-cluster",
			Pools: []collector.PoolMetrics{
				{
					PoolName:       "cpu-pool",
					Replicas:       4,
					GPUUtilization: 0,   // no GPU
					CPUUtilization: 0.7, // 70% CPU -- should NOT scale down
				},
			},
			Timestamp: time.Now(),
		},
	}
	actions, err := opt.Optimize(context.Background(), metrics, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, a := range actions {
		if a.DesiredReplicas < a.CurrentReplicas {
			t.Errorf("should NOT recommend scale-down when CPU utilization (0.7) is above target (0.30), but got DesiredReplicas=%d < CurrentReplicas=%d",
				a.DesiredReplicas, a.CurrentReplicas)
		}
	}
}
