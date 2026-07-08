package collector

import (
	"context"
	"testing"
	"time"
)

func TestCollectAll(t *testing.T) {
	tests := []struct {
		name         string
		wantMinPools int
		wantErr      bool
	}{
		{
			name:         "returns cluster metrics with pool data",
			wantMinPools: 1,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMetricsCollector()
			clusters, err := mc.CollectAll(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(clusters) == 0 {
				t.Fatal("expected at least one cluster, got none")
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
