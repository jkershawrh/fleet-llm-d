package cost

import (
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
)

func TestInferenceClassToPricing(t *testing.T) {
	table := DefaultPricingTable()

	ic := modelplane.InferenceClass{
		Name:    "h200-8gpu",
		GPUType: "H200",
		Count:   8,
		Memory:  141,
	}

	pricing, err := InferenceClassToPricing(ic, table)
	if err != nil {
		t.Fatalf("InferenceClassToPricing: %v", err)
	}

	if pricing.GPUType != "H200" {
		t.Fatalf("GPUType = %q, want 'H200'", pricing.GPUType)
	}
	if pricing.CostPerHour != 4.50 {
		t.Fatalf("CostPerHour = %f, want 4.50", pricing.CostPerHour)
	}
	if pricing.MemoryGB != 141 {
		t.Fatalf("MemoryGB = %d, want 141", pricing.MemoryGB)
	}
	if pricing.PricingTier != "on-demand" {
		t.Fatalf("PricingTier = %q, want 'on-demand'", pricing.PricingTier)
	}
}

func TestInferenceClassToPricing_UnknownGPU(t *testing.T) {
	table := DefaultPricingTable()

	ic := modelplane.InferenceClass{
		Name:    "unknown-gpu",
		GPUType: "TPUv5",
		Count:   4,
		Memory:  64,
	}

	_, err := InferenceClassToPricing(ic, table)
	if err == nil {
		t.Fatal("expected error for unknown GPU type")
	}
}

func TestComputeDeploymentCost(t *testing.T) {
	table := DefaultPricingTable()

	md := modelplane.ModelDeployment{
		Name:     "granite-deploy",
		Model:    "granite-3b",
		Replicas: 4,
		Status: modelplane.DeploymentStatus{
			Phase:    "Running",
			Clusters: []string{"cluster-1"},
		},
	}

	clusters := []modelplane.InferenceCluster{
		{
			Name:   "cluster-1",
			Region: "us-east-1",
			Pools:  []modelplane.NodePool{{Name: "pool-1", GPUType: "H200", Count: 8, Available: 4}},
		},
	}

	cost, err := ComputeDeploymentCost(md, clusters, table)
	if err != nil {
		t.Fatalf("ComputeDeploymentCost: %v", err)
	}

	// Expected: 4 replicas * $4.50/hr = $18.00/hr
	expected := 4 * 4.50
	if cost != expected {
		t.Fatalf("cost = %f, want %f", cost, expected)
	}
}

func TestComputeDeploymentCost_MultiCluster(t *testing.T) {
	table := DefaultPricingTable()

	md := modelplane.ModelDeployment{
		Name:     "multi-deploy",
		Model:    "llama-70b",
		Replicas: 6,
		Status: modelplane.DeploymentStatus{
			Phase:    "Running",
			Clusters: []string{"cluster-1", "cluster-2"},
		},
	}

	clusters := []modelplane.InferenceCluster{
		{
			Name:  "cluster-1",
			Pools: []modelplane.NodePool{{Name: "pool-1", GPUType: "H200", Count: 8, Available: 4}},
		},
		{
			Name:  "cluster-2",
			Pools: []modelplane.NodePool{{Name: "pool-2", GPUType: "A100", Count: 8, Available: 6}},
		},
	}

	cost, err := ComputeDeploymentCost(md, clusters, table)
	if err != nil {
		t.Fatalf("ComputeDeploymentCost: %v", err)
	}

	// 6 replicas across 2 clusters: 3 on H200 ($4.50) + 3 on A100 ($3.20)
	// = 3*4.50 + 3*3.20 = 13.50 + 9.60 = $23.10
	expected := 3*4.50 + 3*3.20
	if cost < expected-0.01 || cost > expected+0.01 {
		t.Fatalf("cost = %f, want %f", cost, expected)
	}
}

func TestComputeDeploymentCost_HandlesUnknownClusters(t *testing.T) {
	md := modelplane.ModelDeployment{
		Name: "test-deploy", Model: "test-model", Replicas: 6,
		Status: modelplane.DeploymentStatus{
			Clusters: []string{"known-cluster", "unknown-cluster"},
		},
	}
	clusters := []modelplane.InferenceCluster{{
		Name:  "known-cluster",
		Pools: []modelplane.NodePool{{Name: "pool-1", GPUType: "A100", Count: 8, Available: 4}},
	}}
	pricing := DefaultPricingTable()

	cost, err := ComputeDeploymentCost(md, clusters, pricing)
	if err != nil {
		t.Fatal(err)
	}

	// All 6 replicas should be costed on the known cluster
	// (not divided between 2 clusters with 3 silently dropped)
	expectedPerReplica := 3.20 // A100 on-demand
	expectedTotal := 6.0 * expectedPerReplica
	if cost != expectedTotal {
		t.Errorf("expected cost %.2f (all replicas on known cluster), got %.2f", expectedTotal, cost)
	}
}

func TestOptimizePlacement_SortedByCost(t *testing.T) {
	table := DefaultPricingTable()

	md := modelplane.ModelDeployment{
		Name:     "test-deploy",
		Model:    "test-model",
		Replicas: 2,
		Status: modelplane.DeploymentStatus{
			Phase:    "Running",
			Clusters: []string{"expensive-cluster"},
		},
	}

	clusters := []modelplane.InferenceCluster{
		{
			Name:  "expensive-cluster",
			Pools: []modelplane.NodePool{{Name: "pool-1", GPUType: "B200", Count: 8, Available: 4}},
		},
		{
			Name:  "cheap-cluster",
			Pools: []modelplane.NodePool{{Name: "pool-2", GPUType: "L40", Count: 8, Available: 6}},
		},
		{
			Name:  "mid-cluster",
			Pools: []modelplane.NodePool{{Name: "pool-3", GPUType: "A100", Count: 8, Available: 4}},
		},
	}

	suggestions := OptimizePlacement(md, clusters, table)

	if len(suggestions) < 3 {
		t.Fatalf("expected at least 3 suggestions, got %d", len(suggestions))
	}

	// Verify sorted by cost ascending
	for i := 1; i < len(suggestions); i++ {
		if suggestions[i].CostPerHour < suggestions[i-1].CostPerHour {
			t.Fatalf("suggestions not sorted by cost: [%d]=%f < [%d]=%f",
				i, suggestions[i].CostPerHour, i-1, suggestions[i-1].CostPerHour)
		}
	}

	// Cheapest should be L40 cluster
	if suggestions[0].Cluster != "cheap-cluster" {
		t.Fatalf("cheapest suggestion cluster = %q, want 'cheap-cluster'", suggestions[0].Cluster)
	}
	if suggestions[0].GPUType != "L40" {
		t.Fatalf("cheapest suggestion GPU = %q, want 'L40'", suggestions[0].GPUType)
	}

	// The suggestion for moving from B200 to L40 should show positive savings
	if suggestions[0].Savings <= 0 {
		t.Fatalf("savings should be positive for cheaper option, got %f", suggestions[0].Savings)
	}
}
