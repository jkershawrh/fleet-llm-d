package solver

import (
	"context"
	"fmt"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

func generateClusters(n int) []ClusterInfo {
	regions := []string{"us-east", "us-west", "eu-west", "ap-south"}
	gpuTypes := []string{"A100", "H100", "A10G"}
	clusters := make([]ClusterInfo, n)
	for i := range clusters {
		region := regions[i%len(regions)]
		clusters[i] = ClusterInfo{
			ID:     fmt.Sprintf("cluster-%d", i),
			Name:   fmt.Sprintf("%s-%d", region, i),
			Region: region,
			Labels: map[string]string{
				"data-sovereignty": region[:2],
				"cost-tier":        []string{"economy", "standard", "premium"}[i%3],
			},
			GPUCapacity: GPUCapacity{
				Available: 4 + i%8,
				Total:     8,
				Types:     []string{gpuTypes[i%len(gpuTypes)]},
			},
			Utilization: float64(i%100) / 100.0,
		}
	}
	return clusters
}

func benchmarkSolve(b *testing.B, clusterCount int) {
	s := NewConstraintSolver()
	ctx := context.Background()
	clusters := generateClusters(clusterCount)

	pool := v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   "llama-3-70b",
			Source: "hf://meta-llama/Llama-3-70B",
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "default",
			MaxClusters: 10,
		},
	}

	policy := v1alpha1.PlacementPolicySpec{
		Constraints: []v1alpha1.PlacementConstraint{
			{Type: "regulatory", Rule: "data-sovereignty=us"},
			{Type: "hardware", Rule: "gpu-type=H100"},
		},
		Affinity: []v1alpha1.AffinityRule{
			{Type: "cost-optimization", Weight: 0.8},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Solve(ctx, pool, clusters, policy)
	}
}

func BenchmarkSolve_10(b *testing.B)   { benchmarkSolve(b, 10) }
func BenchmarkSolve_50(b *testing.B)   { benchmarkSolve(b, 50) }
func BenchmarkSolve_100(b *testing.B)  { benchmarkSolve(b, 100) }
func BenchmarkSolve_250(b *testing.B)  { benchmarkSolve(b, 250) }
func BenchmarkSolve_500(b *testing.B)  { benchmarkSolve(b, 500) }
func BenchmarkSolve_1000(b *testing.B) { benchmarkSolve(b, 1000) }
