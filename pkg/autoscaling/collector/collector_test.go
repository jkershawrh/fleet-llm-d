package collector

import (
	"context"
	"testing"
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
