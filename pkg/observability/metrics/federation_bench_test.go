package metrics

import (
	"context"
	"fmt"
	"testing"
)

func benchmarkFederate(b *testing.B, clusterCount int) {
	f := &InMemoryMetricsFederator{
		clusterMetrics: make(map[string]ClusterMetricsSummary, clusterCount),
		modelMetrics:   make(map[string]*ModelMetrics),
		tenantMetrics:  make(map[string]*TenantMetrics),
	}

	clusterIDs := make([]string, clusterCount)
	for i := range clusterIDs {
		id := fmt.Sprintf("cluster-%d", i)
		clusterIDs[i] = id
		f.clusterMetrics[id] = ClusterMetricsSummary{
			ClusterID:  id,
			GPUs:       8,
			Models:     2 + i%5,
			Throughput: float64(100 + i%500),
			AvgTTFT_Ms: float64(30 + i%100),
		}
	}

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.FederateMetrics(ctx, clusterIDs)
	}
}

func BenchmarkFederateMetrics_10(b *testing.B)   { benchmarkFederate(b, 10) }
func BenchmarkFederateMetrics_50(b *testing.B)   { benchmarkFederate(b, 50) }
func BenchmarkFederateMetrics_100(b *testing.B)  { benchmarkFederate(b, 100) }
func BenchmarkFederateMetrics_250(b *testing.B)  { benchmarkFederate(b, 250) }
func BenchmarkFederateMetrics_500(b *testing.B)  { benchmarkFederate(b, 500) }
func BenchmarkFederateMetrics_1000(b *testing.B) { benchmarkFederate(b, 1000) }
