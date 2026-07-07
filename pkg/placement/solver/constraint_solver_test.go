package solver

import (
	"context"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// helper to build a minimal FleetInferencePoolSpec for tests.
func poolSpec(model, source string) v1alpha1.FleetInferencePoolSpec {
	return v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:   model,
			Source: source,
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "test-policy",
			MinClusters: 1,
			MaxClusters: 3,
		},
		Serving: v1alpha1.ServingSpec{
			InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
				Spec: v1alpha1.InferencePoolTemplateSpec{
					TargetPorts: []int{8080},
				},
			},
		},
	}
}

// sampleClusters returns a realistic set of candidate clusters spread across
// regions and GPU types.
func sampleClusters() []ClusterInfo {
	return []ClusterInfo{
		{
			ID:     "cluster-us-east-1",
			Name:   "us-east-prod",
			Region: "us-east",
			Labels: map[string]string{
				"env":          "production",
				"cost-tier":    "standard",
				"data-sovereignty": "us",
			},
			GPUCapacity: GPUCapacity{
				Available: 4,
				Total:     8,
				Types:     []string{"A100", "H100"},
			},
			Utilization: 0.50,
		},
		{
			ID:     "cluster-eu-west-1",
			Name:   "eu-west-prod",
			Region: "eu-west",
			Labels: map[string]string{
				"env":          "production",
				"cost-tier":    "premium",
				"data-sovereignty": "eu",
			},
			GPUCapacity: GPUCapacity{
				Available: 6,
				Total:     8,
				Types:     []string{"A100"},
			},
			Utilization: 0.25,
		},
		{
			ID:     "cluster-ap-south-1",
			Name:   "ap-south-prod",
			Region: "ap-south",
			Labels: map[string]string{
				"env":          "production",
				"cost-tier":    "economy",
				"data-sovereignty": "ap",
			},
			GPUCapacity: GPUCapacity{
				Available: 8,
				Total:     8,
				Types:     []string{"A10G"},
			},
			Utilization: 0.10,
		},
		{
			ID:     "cluster-us-west-1",
			Name:   "us-west-staging",
			Region: "us-west",
			Labels: map[string]string{
				"env":          "staging",
				"cost-tier":    "economy",
				"data-sovereignty": "us",
			},
			GPUCapacity: GPUCapacity{
				Available: 2,
				Total:     4,
				Types:     []string{"H100"},
			},
			Utilization: 0.75,
		},
	}
}

func TestSolve_RegulatoryConstraint(t *testing.T) {
	tests := []struct {
		name              string
		pool              v1alpha1.FleetInferencePoolSpec
		clusters          []ClusterInfo
		policy            v1alpha1.PlacementPolicySpec
		wantClusterIDs    []string
		wantMinDecisions  int
	}{
		{
			name:     "EU data sovereignty restricts placement to eu-west",
			pool:     poolSpec("llama-3-70b", "huggingface://meta-llama/Llama-3-70B"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "regulatory", Rule: "data-sovereignty=eu"},
				},
			},
			wantClusterIDs:   []string{"cluster-eu-west-1"},
			wantMinDecisions: 1,
		},
		{
			name:     "US data sovereignty allows us-east and us-west clusters",
			pool:     poolSpec("mixtral-8x7b", "huggingface://mistralai/Mixtral-8x7B"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "regulatory", Rule: "data-sovereignty=us"},
				},
			},
			wantClusterIDs:   []string{"cluster-us-east-1", "cluster-us-west-1"},
			wantMinDecisions: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solver := NewConstraintSolver()
			decisions, err := solver.Solve(context.Background(), tt.pool, tt.clusters, tt.policy)
			if err != nil {
				t.Fatalf("Solve() returned error: %v", err)
			}
			if len(decisions) < tt.wantMinDecisions {
				t.Fatalf("expected at least %d decisions, got %d", tt.wantMinDecisions, len(decisions))
			}

			gotIDs := make(map[string]bool)
			for _, d := range decisions {
				gotIDs[d.ClusterID] = true
			}
			for _, wantID := range tt.wantClusterIDs {
				if !gotIDs[wantID] {
					t.Errorf("expected cluster %s in decisions, got %v", wantID, decisions)
				}
			}
		})
	}
}

func TestSolve_HardwareConstraint(t *testing.T) {
	tests := []struct {
		name           string
		pool           v1alpha1.FleetInferencePoolSpec
		clusters       []ClusterInfo
		policy         v1alpha1.PlacementPolicySpec
		wantGPUType    string
		wantClusterIDs []string
	}{
		{
			name:     "H100 requirement filters to clusters with H100 GPUs",
			pool:     poolSpec("llama-3-70b", "huggingface://meta-llama/Llama-3-70B"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "hardware", Rule: "gpu-type=H100"},
				},
			},
			wantGPUType:    "H100",
			wantClusterIDs: []string{"cluster-us-east-1", "cluster-us-west-1"},
		},
		{
			name:     "A10G requirement filters to ap-south cluster only",
			pool:     poolSpec("phi-3-mini", "huggingface://microsoft/Phi-3-mini"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "hardware", Rule: "gpu-type=A10G"},
				},
			},
			wantGPUType:    "A10G",
			wantClusterIDs: []string{"cluster-ap-south-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solver := NewConstraintSolver()
			decisions, err := solver.Solve(context.Background(), tt.pool, tt.clusters, tt.policy)
			if err != nil {
				t.Fatalf("Solve() returned error: %v", err)
			}
			if len(decisions) == 0 {
				t.Fatal("expected at least one placement decision")
			}

			gotIDs := make(map[string]bool)
			for _, d := range decisions {
				gotIDs[d.ClusterID] = true
				if d.GPUType != tt.wantGPUType {
					t.Errorf("decision for cluster %s: got GPU type %q, want %q", d.ClusterID, d.GPUType, tt.wantGPUType)
				}
			}
			for _, wantID := range tt.wantClusterIDs {
				if !gotIDs[wantID] {
					t.Errorf("expected cluster %s in decisions, got IDs %v", wantID, gotIDs)
				}
			}
		})
	}
}

func TestSolve_CostConstraint(t *testing.T) {
	tests := []struct {
		name            string
		pool            v1alpha1.FleetInferencePoolSpec
		clusters        []ClusterInfo
		policy          v1alpha1.PlacementPolicySpec
		wantFirstChoice string
	}{
		{
			name:     "cost optimization prefers economy-tier cluster with lowest utilization",
			pool:     poolSpec("phi-3-mini", "huggingface://microsoft/Phi-3-mini"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Affinity: []v1alpha1.AffinityRule{
					{Type: "cost-optimization", Weight: 1.0},
				},
			},
			// ap-south has cost-tier=economy and utilization=0.10, the cheapest option.
			wantFirstChoice: "cluster-ap-south-1",
		},
		{
			name:     "cost optimization with hardware constraint narrows candidates",
			pool:     poolSpec("llama-3-70b", "huggingface://meta-llama/Llama-3-70B"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "hardware", Rule: "gpu-type=A100"},
				},
				Affinity: []v1alpha1.AffinityRule{
					{Type: "cost-optimization", Weight: 0.8},
				},
			},
			// Among A100 clusters (us-east at 0.50, eu-west at 0.25), eu-west is cheaper.
			wantFirstChoice: "cluster-eu-west-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solver := NewConstraintSolver()
			decisions, err := solver.Solve(context.Background(), tt.pool, tt.clusters, tt.policy)
			if err != nil {
				t.Fatalf("Solve() returned error: %v", err)
			}
			if len(decisions) == 0 {
				t.Fatal("expected at least one placement decision")
			}

			// The first decision should be the top-scored (cheapest) cluster.
			best := decisions[0]
			if best.ClusterID != tt.wantFirstChoice {
				t.Errorf("expected top choice %s, got %s (score %.2f)", tt.wantFirstChoice, best.ClusterID, best.Score)
			}
		})
	}
}

func TestSolve_MultiCluster(t *testing.T) {
	tests := []struct {
		name             string
		pool             v1alpha1.FleetInferencePoolSpec
		clusters         []ClusterInfo
		policy           v1alpha1.PlacementPolicySpec
		wantMinClusters  int
		wantTotalReplicas int
	}{
		{
			name: "spreading across regions distributes replicas",
			pool: func() v1alpha1.FleetInferencePoolSpec {
				p := poolSpec("llama-3-70b", "huggingface://meta-llama/Llama-3-70B")
				p.Placement.MinClusters = 2
				p.Placement.MaxClusters = 4
				return p
			}(),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Spreading: &v1alpha1.SpreadingRule{
					MaxSkew:     1,
					TopologyKey: "region",
				},
			},
			wantMinClusters:   2,
			wantTotalReplicas: 4, // at least one replica per selected cluster
		},
		{
			name: "spreading with constraint limits eligible clusters",
			pool: func() v1alpha1.FleetInferencePoolSpec {
				p := poolSpec("mixtral-8x7b", "huggingface://mistralai/Mixtral-8x7B")
				p.Placement.MinClusters = 2
				p.Placement.MaxClusters = 3
				return p
			}(),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "hardware", Rule: "gpu-type=A100"},
				},
				Spreading: &v1alpha1.SpreadingRule{
					MaxSkew:     1,
					TopologyKey: "region",
				},
			},
			// Only us-east (A100+H100) and eu-west (A100) have A100.
			wantMinClusters:   2,
			wantTotalReplicas: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solver := NewConstraintSolver()
			decisions, err := solver.Solve(context.Background(), tt.pool, tt.clusters, tt.policy)
			if err != nil {
				t.Fatalf("Solve() returned error: %v", err)
			}

			if len(decisions) < tt.wantMinClusters {
				t.Fatalf("expected at least %d cluster decisions, got %d", tt.wantMinClusters, len(decisions))
			}

			totalReplicas := 0
			regions := make(map[string]int)
			for _, d := range decisions {
				totalReplicas += d.Replicas
				// Find the cluster region for skew validation.
				for _, c := range tt.clusters {
					if c.ID == d.ClusterID {
						regions[c.Region] += d.Replicas
					}
				}
			}

			if totalReplicas < tt.wantTotalReplicas {
				t.Errorf("expected at least %d total replicas, got %d", tt.wantTotalReplicas, totalReplicas)
			}

			// Validate max skew when spreading is configured.
			if tt.policy.Spreading != nil {
				minReplicas, maxReplicas := totalReplicas, 0
				for _, count := range regions {
					if count < minReplicas {
						minReplicas = count
					}
					if count > maxReplicas {
						maxReplicas = count
					}
				}
				skew := maxReplicas - minReplicas
				if skew > tt.policy.Spreading.MaxSkew {
					t.Errorf("replica skew %d exceeds maxSkew %d; distribution: %v", skew, tt.policy.Spreading.MaxSkew, regions)
				}
			}
		})
	}
}

func TestSolve_NoFeasiblePlacement(t *testing.T) {
	tests := []struct {
		name     string
		pool     v1alpha1.FleetInferencePoolSpec
		clusters []ClusterInfo
		policy   v1alpha1.PlacementPolicySpec
	}{
		{
			name:     "no clusters match required GPU type",
			pool:     poolSpec("llama-3-70b", "huggingface://meta-llama/Llama-3-70B"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "hardware", Rule: "gpu-type=TPUv5"},
				},
			},
		},
		{
			name:     "regulatory constraint eliminates all clusters",
			pool:     poolSpec("llama-3-70b", "huggingface://meta-llama/Llama-3-70B"),
			clusters: sampleClusters(),
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "regulatory", Rule: "data-sovereignty=cn"},
				},
			},
		},
		{
			name: "empty cluster list",
			pool: poolSpec("phi-3-mini", "huggingface://microsoft/Phi-3-mini"),
			clusters: []ClusterInfo{},
			policy: v1alpha1.PlacementPolicySpec{
				Constraints: []v1alpha1.PlacementConstraint{
					{Type: "hardware", Rule: "gpu-type=A100"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solver := NewConstraintSolver()
			decisions, err := solver.Solve(context.Background(), tt.pool, tt.clusters, tt.policy)
			if err == nil {
				t.Fatalf("expected error for infeasible placement, got decisions: %v", decisions)
			}
			if decisions != nil {
				t.Errorf("expected nil decisions when placement is infeasible, got %v", decisions)
			}
		})
	}
}
