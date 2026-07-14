package controller

import (
	"context"
	"fmt"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

func generateBenchClusters(n int) []solver.ClusterInfo {
	regions := []string{"us-east", "us-west", "eu-west", "ap-south"}
	gpuTypes := []string{"A100", "H100", "A10G"}
	clusters := make([]solver.ClusterInfo, n)
	for i := range clusters {
		region := regions[i%len(regions)]
		clusters[i] = solver.ClusterInfo{
			ID:     fmt.Sprintf("cluster-%d", i),
			Name:   fmt.Sprintf("%s-%d", region, i),
			Region: region,
			Labels: map[string]string{
				"data-sovereignty": region[:2],
				"cost-tier":        []string{"economy", "standard", "premium"}[i%3],
			},
			GPUCapacity: solver.GPUCapacity{
				Available: 4 + i%8,
				Total:     8,
				Types:     []string{gpuTypes[i%len(gpuTypes)]},
			},
			Utilization: float64(i%100) / 100.0,
		}
	}
	return clusters
}

func benchmarkReconcilePool(b *testing.B, clusterCount int) {
	clusters := generateBenchClusters(clusterCount)
	lister := func(_ context.Context) ([]solver.ClusterInfo, error) {
		return clusters, nil
	}

	pool := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   "llama-3-70b",
			Source: "hf://meta-llama/Llama-3-70B",
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "default",
			MaxClusters: 10,
		},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8080},
				},
			},
		},
	}

	r := NewReconciler(solver.NewConstraintSolver(), lister)
	r.SetActualClusterObserver(func(_ context.Context, _ v1alpha1.FleetInferencePoolSpec, desired []string) ([]string, error) {
		return append([]string(nil), desired...), nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.ReconcilePool(context.Background(), pool)
	}
}

func BenchmarkReconcilePool_10(b *testing.B)   { benchmarkReconcilePool(b, 10) }
func BenchmarkReconcilePool_50(b *testing.B)   { benchmarkReconcilePool(b, 50) }
func BenchmarkReconcilePool_100(b *testing.B)  { benchmarkReconcilePool(b, 100) }
func BenchmarkReconcilePool_250(b *testing.B)  { benchmarkReconcilePool(b, 250) }
func BenchmarkReconcilePool_500(b *testing.B)  { benchmarkReconcilePool(b, 500) }
func BenchmarkReconcilePool_1000(b *testing.B) { benchmarkReconcilePool(b, 1000) }
