package optimizer

import (
	"context"
	"fmt"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

func benchmarkOptimize(b *testing.B, clusterCount int) {
	opt := NewFleetOptimizer()
	ctx := context.Background()

	metrics := make([]collector.ClusterMetrics, clusterCount)
	for i := range metrics {
		metrics[i] = collector.ClusterMetrics{
			ClusterID: fmt.Sprintf("cluster-%d", i),
			Pools: []collector.PoolMetrics{{
				PoolName:       fmt.Sprintf("pool-%d", i),
				QueueDepth:     10 + i%50,
				TTFT_P50_Ms:    float64(20 + i%200),
				TTFT_P99_Ms:    float64(100 + i%500),
				Throughput_TPS: float64(10 + i%100),
				GPUUtilization: float64(i%100) / 100.0,
				KVCacheHitRate: float64(50+i%50) / 100.0,
			}},
		}
	}

	policy := v1alpha1.FleetScalingPolicySpec{
		Objectives: []v1alpha1.ScalingObjective{
			{Metric: "queueDepth", Target: "< 20"},
			{Metric: "gpuUtilization", Target: "> 0.3"},
		},
		Constraints: v1alpha1.ScalingConstraints{
			GlobalMaxGPUs:  clusterCount * 8,
			MaxScaleUpRate: 4,
		},
		Strategy: "balanced",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = opt.Optimize(ctx, metrics, policy)
	}
}

func BenchmarkOptimize_10(b *testing.B)   { benchmarkOptimize(b, 10) }
func BenchmarkOptimize_50(b *testing.B)   { benchmarkOptimize(b, 50) }
func BenchmarkOptimize_100(b *testing.B)  { benchmarkOptimize(b, 100) }
func BenchmarkOptimize_250(b *testing.B)  { benchmarkOptimize(b, 250) }
func BenchmarkOptimize_500(b *testing.B)  { benchmarkOptimize(b, 500) }
func BenchmarkOptimize_1000(b *testing.B) { benchmarkOptimize(b, 1000) }
