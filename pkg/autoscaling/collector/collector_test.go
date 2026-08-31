package collector

import (
	"context"
	"testing"
	"time"
)

// TestNewMetricsCollectorStartsEmpty is the regression guard for a collector
// that used to seed itself with an invented "default-cluster" reading. That
// row was indistinguishable from real telemetry to every consumer and never
// expired, so a fresh collector must report exactly what it has: nothing.
func TestNewMetricsCollectorStartsEmpty(t *testing.T) {
	mc := NewMetricsCollector()

	clusters, err := mc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("a new collector reported %d clusters (%+v); it must start empty", len(clusters), clusters)
	}
}

func TestCollectAll(t *testing.T) {
	tests := []struct {
		name         string
		seed         []ClusterMetrics
		wantClusters int
		wantMinPools int
	}{
		{
			name: "returns cluster metrics with pool data",
			seed: []ClusterMetrics{{
				ClusterID: "east",
				Pools: []PoolMetrics{{
					PoolName:       "llama-70b",
					Model:          "llama-70b",
					Replicas:       2,
					TTFT_P99_Ms:    50.0,
					Throughput_TPS: 10.0,
					GPUUtilization: 0.50,
					KVCacheHitRate: 0.80,
				}},
				Timestamp: time.Now(),
			}},
			wantClusters: 1,
			wantMinPools: 1,
		},
		{
			name: "reports every cluster that has sent a sample",
			seed: []ClusterMetrics{
				{ClusterID: "east", Pools: []PoolMetrics{{PoolName: "a"}}, Timestamp: time.Now()},
				{ClusterID: "west", Pools: []PoolMetrics{{PoolName: "a"}}, Timestamp: time.Now()},
			},
			wantClusters: 2,
			wantMinPools: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMetricsCollector()
			for _, sample := range tt.seed {
				mc.Add(sample)
			}

			clusters, err := mc.CollectAll(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(clusters) != tt.wantClusters {
				t.Fatalf("got %d clusters, want %d", len(clusters), tt.wantClusters)
			}

			for _, cluster := range clusters {
				if len(cluster.Pools) < tt.wantMinPools {
					t.Errorf("cluster %s: expected at least %d pools, got %d",
						cluster.ClusterID, tt.wantMinPools, len(cluster.Pools))
				}
			}
		})
	}
}

func TestCollectCluster_NotFound(t *testing.T) {
	tests := []struct {
		name      string
		clusterID string
		wantErr   bool
	}{
		{
			name:      "unknown cluster returns error",
			clusterID: "nonexistent-cluster-id",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMetricsCollector()
			result, err := mc.CollectCluster(context.Background(), tt.clusterID)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestPoolMetrics_HasCPUFields(t *testing.T) {
	mc := NewMetricsCollector()
	imc := mc.(*InMemoryCollector)
	imc.Add(ClusterMetrics{
		ClusterID: "cpu-cluster",
		Pools: []PoolMetrics{
			{
				PoolName:              "cpu-pool",
				CPUUtilization:        0.75,
				InferenceLatencyP99Ms: 450.0,
				QueueDepth:            5,
			},
		},
		Timestamp: time.Now(),
	})
	cm, err := imc.CollectCluster(context.Background(), "cpu-cluster")
	if err != nil {
		t.Fatal(err)
	}
	m := cm.Pools[0]
	if m.CPUUtilization != 0.75 {
		t.Errorf("CPUUtilization = %f, want 0.75", m.CPUUtilization)
	}
	if m.InferenceLatencyP99Ms != 450.0 {
		t.Errorf("InferenceLatencyP99Ms = %f, want 450.0", m.InferenceLatencyP99Ms)
	}
}

func TestPrometheusCollector_Implements_Interface(t *testing.T) {
	pc := NewPrometheusCollector("http://localhost:9090")
	var _ MetricsCollector = pc // compile-time check
	pc.Add(ClusterMetrics{
		ClusterID: "test-cluster",
		Pools: []PoolMetrics{
			{
				PoolName:       "test-pool",
				CPUUtilization: 0.5,
			},
		},
		Timestamp: time.Now(),
	})
	cm, err := pc.CollectCluster(context.Background(), "test-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if cm.Pools[0].CPUUtilization != 0.5 {
		t.Error("expected 0.5")
	}
}
