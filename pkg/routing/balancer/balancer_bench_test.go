package balancer

import (
	"context"
	"fmt"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

func generateCandidates(n int) []policy.ClusterHealth {
	candidates := make([]policy.ClusterHealth, n)
	for i := range candidates {
		candidates[i] = policy.ClusterHealth{
			ClusterID:         fmt.Sprintf("cluster-%d", i),
			Healthy:           true,
			LatencyMs:         float64(10 + i%50),
			CapacityRemaining: float64(i%100) / 100.0,
			KVCacheHitRate:    float64(50+i%50) / 100.0,
			AvailableSlots:    4 + i%8,
			Region:            []string{"us-east", "us-west", "eu-west", "ap-south"}[i%4],
		}
	}
	return candidates
}

func benchmarkWeightedBalancer(b *testing.B, clusterCount int) {
	bal := NewWeightedBalancer()
	ctx := context.Background()
	candidates := generateCandidates(clusterCount)
	request := policy.RoutingRequest{
		Model:        "llama-3-70b",
		TenantID:     "tenant-alpha",
		SourceRegion: "us-east",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bal.SelectCluster(ctx, candidates, request)
	}
}

func BenchmarkWeightedBalancer_10(b *testing.B)   { benchmarkWeightedBalancer(b, 10) }
func BenchmarkWeightedBalancer_50(b *testing.B)   { benchmarkWeightedBalancer(b, 50) }
func BenchmarkWeightedBalancer_100(b *testing.B)  { benchmarkWeightedBalancer(b, 100) }
func BenchmarkWeightedBalancer_250(b *testing.B)  { benchmarkWeightedBalancer(b, 250) }
func BenchmarkWeightedBalancer_500(b *testing.B)  { benchmarkWeightedBalancer(b, 500) }
func BenchmarkWeightedBalancer_1000(b *testing.B) { benchmarkWeightedBalancer(b, 1000) }
