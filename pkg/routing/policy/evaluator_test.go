package policy

import (
	"context"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

func TestEvaluate_PreferLocal(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:        "llama-3-70b",
		TenantID:     "tenant-alpha",
		Headers:      map[string]string{"x-request-id": "req-001"},
		SourceRegion: "us-east",
	}

	clusters := []ClusterHealth{
		{
			ClusterID:         "us-east-cluster-1",
			Healthy:           true,
			LatencyMs:         12,
			CapacityRemaining: 0.65,
			KVCacheHitRate:    0.40,
		},
		{
			ClusterID:         "eu-west-cluster-1",
			Healthy:           true,
			LatencyMs:         85,
			CapacityRemaining: 0.80,
			KVCacheHitRate:    0.55,
		},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					Source: "us-east",
				},
				Action: v1alpha1.RoutingAction{
					PreferLocal: true,
				},
			},
		},
		HealthCheck: &v1alpha1.HealthCheckSpec{
			Interval:           "10s",
			UnhealthyThreshold: 3,
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.TargetCluster != "us-east-cluster-1" {
		t.Errorf("expected target cluster %q, got %q", "us-east-cluster-1", decision.TargetCluster)
	}
}

func TestEvaluate_Failover(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:        "mistral-7b",
		TenantID:     "tenant-beta",
		Headers:      map[string]string{"x-request-id": "req-002"},
		SourceRegion: "us-east",
	}

	clusters := []ClusterHealth{
		{
			ClusterID:         "us-east-cluster-1",
			Healthy:           false,
			LatencyMs:         500,
			CapacityRemaining: 0.0,
			KVCacheHitRate:    0.10,
		},
		{
			ClusterID:         "us-west-cluster-1",
			Healthy:           true,
			LatencyMs:         45,
			CapacityRemaining: 0.72,
			KVCacheHitRate:    0.35,
		},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					Source: "us-east",
				},
				Action: v1alpha1.RoutingAction{
					PreferLocal: true,
					Failover: &v1alpha1.Failover{
						Clusters: []string{"us-west-cluster-1"},
					},
				},
			},
		},
		HealthCheck: &v1alpha1.HealthCheckSpec{
			Interval:           "10s",
			UnhealthyThreshold: 3,
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.TargetCluster != "us-west-cluster-1" {
		t.Errorf("expected failover to %q, got %q", "us-west-cluster-1", decision.TargetCluster)
	}
}

func TestEvaluate_KVCacheAffinity(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:        "llama-3-70b",
		TenantID:     "tenant-gamma",
		Headers:      map[string]string{"x-session-id": "sess-abc123"},
		SourceRegion: "us-east",
	}

	clusters := []ClusterHealth{
		{
			ClusterID:         "us-east-cluster-1",
			Healthy:           true,
			LatencyMs:         15,
			CapacityRemaining: 0.50,
			KVCacheHitRate:    0.30,
		},
		{
			ClusterID:         "us-east-cluster-2",
			Healthy:           true,
			LatencyMs:         18,
			CapacityRemaining: 0.45,
			KVCacheHitRate:    0.92,
		},
		{
			ClusterID:         "us-west-cluster-1",
			Healthy:           true,
			LatencyMs:         42,
			CapacityRemaining: 0.70,
			KVCacheHitRate:    0.60,
		},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					Headers: map[string]string{"x-session-id": "*"},
				},
				Action: v1alpha1.RoutingAction{
					KVCacheAffinity: true,
				},
			},
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.TargetCluster != "us-east-cluster-2" {
		t.Errorf("expected cluster with highest KV cache hit rate %q, got %q", "us-east-cluster-2", decision.TargetCluster)
	}
}

func TestEvaluate_CostOptimized(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:        "phi-3-mini",
		TenantID:     "tenant-delta",
		Headers:      map[string]string{"x-request-id": "req-004"},
		SourceRegion: "eu-west",
	}

	clusters := []ClusterHealth{
		{
			ClusterID:         "us-east-cluster-1",
			Healthy:           true,
			LatencyMs:         90,
			CapacityRemaining: 0.30,
			KVCacheHitRate:    0.25,
		},
		{
			ClusterID:         "eu-west-cluster-1",
			Healthy:           true,
			LatencyMs:         10,
			CapacityRemaining: 0.40,
			KVCacheHitRate:    0.50,
		},
		{
			ClusterID:         "ap-south-cluster-1",
			Healthy:           true,
			LatencyMs:         120,
			CapacityRemaining: 0.90,
			KVCacheHitRate:    0.15,
		},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "cost-optimized",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{},
				Action: v1alpha1.RoutingAction{
					PreferCheapest: true,
					MaxLatencyMs:   200,
				},
			},
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The cluster with the highest remaining capacity is the cheapest to run on.
	if decision.TargetCluster != "ap-south-cluster-1" {
		t.Errorf("expected cheapest cluster %q, got %q", "ap-south-cluster-1", decision.TargetCluster)
	}
}

func TestEvaluate_SemanticTierMatch(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:          "auto",
		SemanticLabel:  "REASONING",
		SemanticScore:  0.999,
		SemanticMargin: 1.235,
	}

	clusters := []ClusterHealth{
		{ClusterID: "cpu-cluster", Healthy: true, CapacityRemaining: 0.8},
		{ClusterID: "gpu-cluster", Healthy: true, CapacityRemaining: 0.6},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					SemanticTier: "REASONING",
				},
				Action: v1alpha1.RoutingAction{
					TargetModelTier: "gpu-large",
				},
			},
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Reason != "semantic-tier:gpu-large" {
		t.Errorf("expected reason %q, got %q", "semantic-tier:gpu-large", decision.Reason)
	}
	if decision.HeadersToInject["X-Semantic-Label"] != "REASONING" {
		t.Errorf("expected X-Semantic-Label header REASONING, got %q", decision.HeadersToInject["X-Semantic-Label"])
	}
}

func TestEvaluate_SemanticTierNoMatch(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:          "auto",
		SemanticLabel:  "SIMPLE",
		SemanticScore:  0.999,
		SemanticMargin: 1.074,
	}

	clusters := []ClusterHealth{
		{ClusterID: "cpu-cluster", Healthy: true, CapacityRemaining: 0.8},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					SemanticTier: "REASONING",
				},
				Action: v1alpha1.RoutingAction{
					TargetModelTier: "gpu-large",
				},
			},
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SIMPLE doesn't match REASONING rule, falls through to default-healthy
	if decision.Reason != "default-healthy" {
		t.Errorf("expected reason %q, got %q", "default-healthy", decision.Reason)
	}
}

func TestEvaluate_SemanticMinConfidence(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	request := RoutingRequest{
		Model:          "auto",
		SemanticLabel:  "REASONING",
		SemanticScore:  0.682,
		SemanticMargin: 0.237, // low confidence
	}

	clusters := []ClusterHealth{
		{ClusterID: "gpu-cluster", Healthy: true, CapacityRemaining: 0.6},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					SemanticTier:  "REASONING",
					MinConfidence: 0.5, // requires margin > 0.5
				},
				Action: v1alpha1.RoutingAction{
					TargetModelTier: "gpu-large",
				},
			},
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Margin 0.237 < MinConfidence 0.5, so rule doesn't match
	if decision.Reason != "default-healthy" {
		t.Errorf("expected reason %q, got %q", "default-healthy", decision.Reason)
	}
}

func TestEvaluate_NoSemanticFieldsPassThrough(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	// Request with no semantic fields — existing behavior unchanged
	request := RoutingRequest{
		Model:        "llama-3-70b",
		SourceRegion: "us-east",
	}

	clusters := []ClusterHealth{
		{ClusterID: "us-east-cluster", Healthy: true, CapacityRemaining: 0.7},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match: v1alpha1.RoutingMatch{
					Source: "us-east",
				},
				Action: v1alpha1.RoutingAction{
					PreferLocal: true,
				},
			},
		},
	}

	decision, err := evaluator.Evaluate(context.Background(), request, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Reason != "prefer-local" {
		t.Errorf("expected reason %q, got %q", "prefer-local", decision.Reason)
	}
}

func TestEvaluate_SemanticWithMultipleRules(t *testing.T) {
	evaluator := NewRoutingPolicyEvaluator()

	clusters := []ClusterHealth{
		{ClusterID: "cpu-small", Healthy: true, CapacityRemaining: 0.9},
		{ClusterID: "gpu-large", Healthy: true, CapacityRemaining: 0.5},
	}

	policy := v1alpha1.FleetRoutingPolicySpec{
		Strategy: "rules-based",
		Rules: []v1alpha1.RoutingRule{
			{
				Match:  v1alpha1.RoutingMatch{SemanticTier: "REASONING"},
				Action: v1alpha1.RoutingAction{TargetModelTier: "gpu-large"},
			},
			{
				Match:  v1alpha1.RoutingMatch{SemanticTier: "COMPLEX"},
				Action: v1alpha1.RoutingAction{TargetModelTier: "gpu-large"},
			},
			{
				Match:  v1alpha1.RoutingMatch{},
				Action: v1alpha1.RoutingAction{PreferCheapest: true},
			},
		},
	}

	// REASONING request hits first rule
	decision, err := evaluator.Evaluate(context.Background(), RoutingRequest{
		SemanticLabel: "REASONING", SemanticMargin: 1.2,
	}, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Reason != "semantic-tier:gpu-large" {
		t.Errorf("REASONING: expected semantic-tier:gpu-large, got %q", decision.Reason)
	}

	// SIMPLE request falls through to catch-all
	decision, err = evaluator.Evaluate(context.Background(), RoutingRequest{
		SemanticLabel: "SIMPLE", SemanticMargin: 1.0,
	}, clusters, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Reason != "prefer-cheapest" {
		t.Errorf("SIMPLE: expected prefer-cheapest, got %q", decision.Reason)
	}
}
