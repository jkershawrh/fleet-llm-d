package balancer

import (
	"context"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

func TestWeightedBalancer(t *testing.T) {
	balancer := NewWeightedBalancer()
	ctx := context.Background()

	candidates := []policy.ClusterHealth{
		{
			ClusterID:         "us-east-1-gpu-a",
			Healthy:           true,
			LatencyMs:         25,
			CapacityRemaining: 0.75,
			KVCacheHitRate:    0.92,
		},
		{
			ClusterID:         "us-west-2-gpu-b",
			Healthy:           true,
			LatencyMs:         40,
			CapacityRemaining: 0.30,
			KVCacheHitRate:    0.85,
		},
		{
			ClusterID:         "eu-west-1-gpu-c",
			Healthy:           true,
			LatencyMs:         60,
			CapacityRemaining: 0.10,
			KVCacheHitRate:    0.70,
		},
	}

	request := policy.RoutingRequest{
		Model:        "llama-3-70b",
		TenantID:     "tenant-alpha",
		SourceRegion: "us-east-1",
	}

	tests := []struct {
		name       string
		candidates []policy.ClusterHealth
		request    policy.RoutingRequest
	}{
		{
			name:       "selects from candidates with varied capacity",
			candidates: candidates,
			request:    request,
		},
		{
			name:       "selects from single candidate",
			candidates: candidates[:1],
			request:    request,
		},
		{
			name: "prefers higher remaining capacity",
			candidates: []policy.ClusterHealth{
				{ClusterID: "low-cap", Healthy: true, CapacityRemaining: 0.05, LatencyMs: 10},
				{ClusterID: "high-cap", Healthy: true, CapacityRemaining: 0.95, LatencyMs: 10},
			},
			request: request,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusterID, err := balancer.SelectCluster(ctx, tc.candidates, tc.request)
			if err != nil {
				t.Fatalf("SelectCluster returned error: %v", err)
			}
			if clusterID == "" {
				t.Fatal("SelectCluster returned empty cluster ID")
			}

			valid := false
			for _, c := range tc.candidates {
				if c.ClusterID == clusterID {
					valid = true
					break
				}
			}
			if !valid {
				t.Errorf("SelectCluster returned %q which is not among candidates", clusterID)
			}
		})
	}
}

func TestLatencyAwareBalancer(t *testing.T) {
	balancer := NewLatencyAwareBalancer()
	ctx := context.Background()

	tests := []struct {
		name            string
		candidates      []policy.ClusterHealth
		request         policy.RoutingRequest
		expectedCluster string
	}{
		{
			name: "selects lowest latency cluster",
			candidates: []policy.ClusterHealth{
				{ClusterID: "high-latency", Healthy: true, LatencyMs: 120, CapacityRemaining: 0.50, KVCacheHitRate: 0.80},
				{ClusterID: "low-latency", Healthy: true, LatencyMs: 8, CapacityRemaining: 0.50, KVCacheHitRate: 0.90},
				{ClusterID: "mid-latency", Healthy: true, LatencyMs: 45, CapacityRemaining: 0.50, KVCacheHitRate: 0.85},
			},
			request:         policy.RoutingRequest{Model: "mistral-7b", TenantID: "tenant-beta", SourceRegion: "us-east-1"},
			expectedCluster: "low-latency",
		},
		{
			name: "selects among closely matched latencies",
			candidates: []policy.ClusterHealth{
				{ClusterID: "cluster-a", Healthy: true, LatencyMs: 15, CapacityRemaining: 0.60, KVCacheHitRate: 0.88},
				{ClusterID: "cluster-b", Healthy: true, LatencyMs: 12, CapacityRemaining: 0.40, KVCacheHitRate: 0.92},
				{ClusterID: "cluster-c", Healthy: true, LatencyMs: 14, CapacityRemaining: 0.55, KVCacheHitRate: 0.90},
			},
			request:         policy.RoutingRequest{Model: "llama-3-8b", TenantID: "tenant-gamma", SourceRegion: "us-east-1"},
			expectedCluster: "cluster-b",
		},
		{
			name: "single candidate returns that cluster",
			candidates: []policy.ClusterHealth{
				{ClusterID: "only-one", Healthy: true, LatencyMs: 50, CapacityRemaining: 0.30, KVCacheHitRate: 0.75},
			},
			request:         policy.RoutingRequest{Model: "llama-3-70b", TenantID: "tenant-delta", SourceRegion: "us-west-2"},
			expectedCluster: "only-one",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusterID, err := balancer.SelectCluster(ctx, tc.candidates, tc.request)
			if err != nil {
				t.Fatalf("SelectCluster returned error: %v", err)
			}
			if clusterID != tc.expectedCluster {
				t.Errorf("expected cluster %q, got %q", tc.expectedCluster, clusterID)
			}
		})
	}
}

func TestCostAwareBalancer(t *testing.T) {
	balancer := NewCostAwareBalancer()
	ctx := context.Background()

	tests := []struct {
		name            string
		candidates      []policy.ClusterHealth
		request         policy.RoutingRequest
		expectedCluster string
	}{
		{
			name: "selects cluster with most remaining capacity as cost proxy",
			candidates: []policy.ClusterHealth{
				{ClusterID: "premium-gpu", Healthy: true, LatencyMs: 10, CapacityRemaining: 0.20, KVCacheHitRate: 0.95},
				{ClusterID: "budget-gpu", Healthy: true, LatencyMs: 30, CapacityRemaining: 0.85, KVCacheHitRate: 0.70},
				{ClusterID: "standard-gpu", Healthy: true, LatencyMs: 20, CapacityRemaining: 0.50, KVCacheHitRate: 0.80},
			},
			request:         policy.RoutingRequest{Model: "llama-3-70b", TenantID: "tenant-epsilon", SourceRegion: "us-east-1"},
			expectedCluster: "budget-gpu",
		},
		{
			name: "selects highest KV cache hit rate for cost efficiency",
			candidates: []policy.ClusterHealth{
				{ClusterID: "cluster-x", Healthy: true, LatencyMs: 20, CapacityRemaining: 0.50, KVCacheHitRate: 0.60},
				{ClusterID: "cluster-y", Healthy: true, LatencyMs: 20, CapacityRemaining: 0.50, KVCacheHitRate: 0.95},
				{ClusterID: "cluster-z", Healthy: true, LatencyMs: 20, CapacityRemaining: 0.50, KVCacheHitRate: 0.40},
			},
			request:         policy.RoutingRequest{Model: "mistral-7b", TenantID: "tenant-zeta", SourceRegion: "us-east-1"},
			expectedCluster: "cluster-y",
		},
		{
			name: "handles large batch request cost optimization",
			candidates: []policy.ClusterHealth{
				{ClusterID: "spot-a100", Healthy: true, LatencyMs: 50, CapacityRemaining: 0.90, KVCacheHitRate: 0.88},
				{ClusterID: "ondemand-h100", Healthy: true, LatencyMs: 12, CapacityRemaining: 0.40, KVCacheHitRate: 0.92},
			},
			request:         policy.RoutingRequest{Model: "llama-3-70b", TenantID: "batch-tenant", SourceRegion: ""},
			expectedCluster: "spot-a100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusterID, err := balancer.SelectCluster(ctx, tc.candidates, tc.request)
			if err != nil {
				t.Fatalf("SelectCluster returned error: %v", err)
			}
			if clusterID != tc.expectedCluster {
				t.Errorf("expected cluster %q, got %q", tc.expectedCluster, clusterID)
			}
		})
	}
}
