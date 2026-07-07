package scorer

import (
	"context"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

// testCluster returns a realistic ClusterInfo for use in scorer tests.
func testCluster(name, region string, availGPU, totalGPU int, utilization float64) solver.ClusterInfo {
	return solver.ClusterInfo{
		ID:     name + "-id",
		Name:   name,
		Region: region,
		Labels: map[string]string{
			"topology.kubernetes.io/region": region,
			"gpu.nvidia.com/class":          "A100",
		},
		GPUCapacity: solver.GPUCapacity{
			Available: availGPU,
			Total:     totalGPU,
			Types:     []string{"A100"},
		},
		Utilization: utilization,
	}
}

// testPool returns a FleetInferencePoolSpec with sensible defaults for tests.
func testPool() v1alpha1.FleetInferencePoolSpec {
	return v1alpha1.FleetInferencePoolSpec{
		Model: v1alpha1.ModelSpec{
			Name:    "llama-3-70b",
			Source:  "huggingface://meta-llama/Llama-3-70B",
			Version: "v1",
		},
		Placement: v1alpha1.PlacementRef{
			PolicyRef:   "cost-aware",
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

// testPolicy returns a PlacementPolicySpec with constraints and affinity rules.
func testPolicy() v1alpha1.PlacementPolicySpec {
	return v1alpha1.PlacementPolicySpec{
		Constraints: []v1alpha1.PlacementConstraint{
			{Type: "gpu", Rule: "require A100"},
		},
		Affinity: []v1alpha1.AffinityRule{
			{Type: "region", Weight: 0.6},
			{Type: "kvCache", Weight: 0.4},
		},
		Spreading: &v1alpha1.SpreadingRule{
			MaxSkew:     1,
			TopologyKey: "topology.kubernetes.io/region",
		},
	}
}

func TestCompositeScorer(t *testing.T) {
	tests := []struct {
		name      string
		scorers   []WeightedScorer
		cluster   solver.ClusterInfo
		wantScore float64
		wantErr   bool
	}{
		{
			name: "cost and capacity weighted equally",
			scorers: []WeightedScorer{
				{Scorer: NewCostScorer(), Weight: 0.5},
				{Scorer: NewCapacityScorer(), Weight: 0.5},
			},
			cluster:   testCluster("us-east-1a", "us-east-1", 4, 8, 0.5),
			wantScore: 0.5, // expected combined weighted score once implemented
			wantErr:   false,
		},
		{
			name: "three scorers with varied weights",
			scorers: []WeightedScorer{
				{Scorer: NewCostScorer(), Weight: 0.3},
				{Scorer: NewCapacityScorer(), Weight: 0.4},
				{Scorer: NewLocalityScorer(), Weight: 0.3},
			},
			cluster:   testCluster("eu-west-1a", "eu-west-1", 6, 8, 0.25),
			wantScore: 0.7, // expected combined weighted score once implemented
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composite := NewCompositeScorer(tt.scorers)
			score, err := composite.Score(context.Background(), tt.cluster, testPool(), testPolicy())
			if tt.wantErr && err == nil {
				t.Fatalf("expected error but got score=%f", score)
			}
			if !tt.wantErr && err != nil {
				// Red phase: all implementations return "not implemented",
				// so this branch fires. When implementations are filled in
				// these tests should pass.
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.wantErr && score != tt.wantScore {
				t.Errorf("score = %f, want %f", score, tt.wantScore)
			}
		})
	}
}

func TestCostScorer(t *testing.T) {
	tests := []struct {
		name      string
		cluster   solver.ClusterInfo
		wantScore float64
		wantErr   bool
	}{
		{
			name:      "low utilization cluster is cheaper",
			cluster:   testCluster("us-west-2a", "us-west-2", 7, 8, 0.1),
			wantScore: 0.9,
			wantErr:   false,
		},
		{
			name:      "high utilization cluster is more expensive",
			cluster:   testCluster("ap-south-1a", "ap-south-1", 1, 8, 0.9),
			wantScore: 0.2,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewCostScorer()
			score, err := scorer.Score(context.Background(), tt.cluster, testPool(), testPolicy())
			if tt.wantErr && err == nil {
				t.Fatalf("expected error but got score=%f", score)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.wantErr && score != tt.wantScore {
				t.Errorf("score = %f, want %f", score, tt.wantScore)
			}
		})
	}
}

func TestCapacityScorer(t *testing.T) {
	tests := []struct {
		name      string
		cluster   solver.ClusterInfo
		wantScore float64
		wantErr   bool
	}{
		{
			name:      "cluster with plenty of available GPUs",
			cluster:   testCluster("us-east-1b", "us-east-1", 6, 8, 0.25),
			wantScore: 0.75, // 6/8 available
			wantErr:   false,
		},
		{
			name:      "nearly full cluster scores low",
			cluster:   testCluster("eu-central-1a", "eu-central-1", 1, 8, 0.88),
			wantScore: 0.125, // 1/8 available
			wantErr:   false,
		},
		{
			name:      "empty cluster scores highest",
			cluster:   testCluster("us-west-2b", "us-west-2", 8, 8, 0.0),
			wantScore: 1.0,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewCapacityScorer()
			score, err := scorer.Score(context.Background(), tt.cluster, testPool(), testPolicy())
			if tt.wantErr && err == nil {
				t.Fatalf("expected error but got score=%f", score)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.wantErr && score != tt.wantScore {
				t.Errorf("score = %f, want %f", score, tt.wantScore)
			}
		})
	}
}
