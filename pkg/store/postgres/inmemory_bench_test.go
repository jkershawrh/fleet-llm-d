package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func benchmarkInMemoryList(b *testing.B, clusterCount int) {
	repo := NewInMemoryClusterRepository()
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < clusterCount; i++ {
		region := []string{"us-east", "us-west", "eu-west", "ap-south"}[i%4]
		_ = repo.Create(ctx, ClusterRecord{
			ID:           fmt.Sprintf("cluster-%d", i),
			Name:         fmt.Sprintf("cluster-%s-%d", region, i),
			Region:       region,
			Labels:       map[string]string{"tier": []string{"edge", "hub", "core"}[i%3]},
			GPUAvailable: 4,
			GPUTotal:     8,
			Status:       "ready",
			RegisteredAt: now,
			UpdatedAt:    now,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = repo.List(ctx)
	}
}

func BenchmarkInMemoryList_10(b *testing.B)   { benchmarkInMemoryList(b, 10) }
func BenchmarkInMemoryList_50(b *testing.B)   { benchmarkInMemoryList(b, 50) }
func BenchmarkInMemoryList_100(b *testing.B)  { benchmarkInMemoryList(b, 100) }
func BenchmarkInMemoryList_250(b *testing.B)  { benchmarkInMemoryList(b, 250) }
func BenchmarkInMemoryList_500(b *testing.B)  { benchmarkInMemoryList(b, 500) }
func BenchmarkInMemoryList_1000(b *testing.B) { benchmarkInMemoryList(b, 1000) }
