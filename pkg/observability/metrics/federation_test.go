package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

func newTestCollector() collector.MetricsCollector {
	col := collector.NewMetricsCollector()
	col.Add(collector.ClusterMetrics{
		ClusterID: "us-east-1",
		Pools: []collector.PoolMetrics{
			{PoolName: "granite-3b", Model: "granite-3b", Throughput_TPS: 300, TTFT_P50_Ms: 45, TTFT_P99_Ms: 250, KVCacheHitRate: 0.85},
			{PoolName: "llama-70b", Model: "llama-70b", Throughput_TPS: 200, TTFT_P50_Ms: 150, TTFT_P99_Ms: 800, KVCacheHitRate: 0.70},
		},
		Timestamp: time.Now(),
	})
	col.Add(collector.ClusterMetrics{
		ClusterID: "us-west-2",
		Pools: []collector.PoolMetrics{
			{PoolName: "granite-3b", Model: "granite-3b", Throughput_TPS: 500, TTFT_P50_Ms: 40, TTFT_P99_Ms: 200, KVCacheHitRate: 0.90},
		},
		Timestamp: time.Now(),
	})
	col.Add(collector.ClusterMetrics{
		ClusterID: "eu-central-1",
		Pools: []collector.PoolMetrics{
			{PoolName: "granite-3b", Model: "granite-3b", Throughput_TPS: 500, TTFT_P50_Ms: 50, TTFT_P99_Ms: 300, KVCacheHitRate: 0.80},
		},
		Timestamp: time.Now(),
	})
	return col
}

func TestFederateMetrics(t *testing.T) {
	tests := []struct {
		name             string
		clusters         []string
		wantThroughput   float64
		wantClusterCount int
	}{
		{
			name:             "federate metrics from multiple clusters",
			clusters:         []string{"us-east-1", "us-west-2", "eu-central-1"},
			wantThroughput:   1500.0,
			wantClusterCount: 3,
		},
		{
			name:             "federate metrics from single cluster",
			clusters:         []string{"us-east-1"},
			wantThroughput:   500.0,
			wantClusterCount: 1,
		},
		{
			name:             "unknown cluster returns empty summary",
			clusters:         []string{"unknown-cluster"},
			wantThroughput:   0,
			wantClusterCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fed := NewMetricsFederator(newTestCollector())
			result, err := fed.FederateMetrics(context.Background(), tt.clusters)
			if err != nil {
				t.Fatalf("FederateMetrics() returned error: %v", err)
			}
			if result == nil {
				t.Fatal("FederateMetrics() returned nil result")
			}
			if result.TotalThroughput != tt.wantThroughput {
				t.Errorf("TotalThroughput = %f, want %f", result.TotalThroughput, tt.wantThroughput)
			}
			if len(result.Clusters) != tt.wantClusterCount {
				t.Errorf("Clusters count = %d, want %d", len(result.Clusters), tt.wantClusterCount)
			}
		})
	}
}

func TestGetModelMetrics(t *testing.T) {
	fed := NewMetricsFederator(newTestCollector())

	t.Run("get metrics for granite model", func(t *testing.T) {
		result, err := fed.GetModelMetrics(context.Background(), "granite-3b")
		if err != nil {
			t.Fatalf("GetModelMetrics() returned error: %v", err)
		}
		if result.Model != "granite-3b" {
			t.Errorf("Model = %s, want granite-3b", result.Model)
		}
		if result.Throughput != 1300.0 {
			t.Errorf("Throughput = %f, want 1300", result.Throughput)
		}
		if len(result.Clusters) != 3 {
			t.Errorf("Clusters count = %d, want 3", len(result.Clusters))
		}
	})

	t.Run("get metrics for llama model", func(t *testing.T) {
		result, err := fed.GetModelMetrics(context.Background(), "llama-70b")
		if err != nil {
			t.Fatalf("GetModelMetrics() returned error: %v", err)
		}
		if result.Model != "llama-70b" {
			t.Errorf("Model = %s, want llama-70b", result.Model)
		}
		if len(result.Clusters) != 1 {
			t.Errorf("Clusters count = %d, want 1", len(result.Clusters))
		}
	})

	t.Run("unknown model returns error", func(t *testing.T) {
		_, err := fed.GetModelMetrics(context.Background(), "nonexistent")
		if err == nil {
			t.Error("expected error for unknown model")
		}
	})
}

func TestGetTenantMetrics(t *testing.T) {
	fed := NewMetricsFederator(newTestCollector())

	_, err := fed.GetTenantMetrics(context.Background(), "unknown-tenant")
	if err == nil {
		t.Error("expected error for unknown tenant")
	}
}

func TestFederateMetricsWithoutCollector(t *testing.T) {
	fed := NewMetricsFederator()
	result, err := fed.FederateMetrics(context.Background(), []string{"any-cluster"})
	if err != nil {
		t.Fatalf("FederateMetrics() should not error without collector: %v", err)
	}
	if len(result.Clusters) != 1 {
		t.Errorf("expected 1 cluster summary, got %d", len(result.Clusters))
	}
}
