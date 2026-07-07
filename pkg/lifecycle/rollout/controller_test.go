package rollout

import (
	"context"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

func TestCreateRollout_Canary(t *testing.T) {
	tests := []struct {
		name          string
		lifecycle     v1alpha1.ModelLifecycleSpec
		wantPhase     string
		wantWeight    int
		wantErr       bool
	}{
		{
			name: "canary rollout with initial weight 10",
			lifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "llama-3-70b",
					Version: "v2",
				},
				FleetPoolRef: "production-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:   10,
						WeightIncrement: 20,
						Interval:        "5m",
						SLOGate: &v1alpha1.SLOGate{
							MaxTTFTRegression:    "10%",
							MaxErrorRateIncrease: "1%",
						},
						RollbackOnFailure: true,
					},
				},
				Clusters: &v1alpha1.ClusterOrder{
					Order: []string{"us-east-1", "eu-west-1", "ap-south-1"},
				},
			},
			wantPhase:  "Canary",
			wantWeight: 10,
			wantErr:    false,
		},
		{
			name: "canary rollout with initial weight 5 and no SLO gate",
			lifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "mistral-7b",
					Version: "v1",
				},
				FleetPoolRef: "staging-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:     5,
						WeightIncrement:   15,
						Interval:          "10m",
						RollbackOnFailure: false,
					},
				},
			},
			wantPhase:  "Canary",
			wantWeight: 5,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := NewRolloutController()
			state, err := ctrl.CreateRollout(context.Background(), tt.lifecycle)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if state.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", state.Phase, tt.wantPhase)
			}

			if state.CurrentWeight != tt.wantWeight {
				t.Errorf("currentWeight = %d, want %d", state.CurrentWeight, tt.wantWeight)
			}

			if state.ID == "" {
				t.Error("expected non-empty rollout ID")
			}
		})
	}
}

func TestAdvanceRollout(t *testing.T) {
	tests := []struct {
		name            string
		setupLifecycle  v1alpha1.ModelLifecycleSpec
		wantWeightAfter int
		wantErr         bool
	}{
		{
			name: "advance canary from 10 to 30",
			setupLifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "llama-3-70b",
					Version: "v2",
				},
				FleetPoolRef: "production-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:   10,
						WeightIncrement: 20,
						Interval:        "5m",
						SLOGate: &v1alpha1.SLOGate{
							MaxTTFTRegression:    "10%",
							MaxErrorRateIncrease: "1%",
						},
						RollbackOnFailure: true,
					},
				},
			},
			wantWeightAfter: 30,
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := NewRolloutController()

			// First create the rollout.
			created, err := ctrl.CreateRollout(context.Background(), tt.setupLifecycle)
			if err != nil {
				t.Fatalf("setup: CreateRollout failed: %v", err)
			}

			// Then advance it.
			advanced, err := ctrl.AdvanceRollout(context.Background(), created.ID)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if advanced.CurrentWeight != tt.wantWeightAfter {
				t.Errorf("weight after advance = %d, want %d", advanced.CurrentWeight, tt.wantWeightAfter)
			}
		})
	}
}

func TestRollback(t *testing.T) {
	tests := []struct {
		name           string
		setupLifecycle v1alpha1.ModelLifecycleSpec
		wantPhase      string
		wantErr        bool
	}{
		{
			name: "rollback sets phase to RolledBack",
			setupLifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "llama-3-70b",
					Version: "v2",
				},
				FleetPoolRef: "production-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:     10,
						WeightIncrement:   20,
						Interval:          "5m",
						RollbackOnFailure: true,
					},
				},
			},
			wantPhase: "RolledBack",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := NewRolloutController()

			// First create the rollout.
			created, err := ctrl.CreateRollout(context.Background(), tt.setupLifecycle)
			if err != nil {
				t.Fatalf("setup: CreateRollout failed: %v", err)
			}

			// Then rollback.
			rolledBack, err := ctrl.RollbackRollout(context.Background(), created.ID)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if rolledBack.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", rolledBack.Phase, tt.wantPhase)
			}
		})
	}
}

func TestSLOGatePass(t *testing.T) {
	tests := []struct {
		name            string
		setupLifecycle  v1alpha1.ModelLifecycleSpec
		wantWeightAfter int
		wantErr         bool
	}{
		{
			name: "SLO gate passes allows advancement",
			setupLifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "llama-3-70b",
					Version: "v2",
				},
				FleetPoolRef: "production-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:   10,
						WeightIncrement: 20,
						Interval:        "5m",
						SLOGate: &v1alpha1.SLOGate{
							MaxTTFTRegression:    "10%",
							MaxErrorRateIncrease: "1%",
						},
						RollbackOnFailure: true,
					},
				},
				Clusters: &v1alpha1.ClusterOrder{
					Order: []string{"us-east-1", "eu-west-1"},
				},
			},
			wantWeightAfter: 30,
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := NewRolloutController()

			// Create the rollout with SLO gate configured.
			created, err := ctrl.CreateRollout(context.Background(), tt.setupLifecycle)
			if err != nil {
				t.Fatalf("setup: CreateRollout failed: %v", err)
			}

			// Advance -- SLO gate should pass and allow weight increase.
			advanced, err := ctrl.AdvanceRollout(context.Background(), created.ID)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if advanced.CurrentWeight != tt.wantWeightAfter {
				t.Errorf("weight after SLO-gated advance = %d, want %d", advanced.CurrentWeight, tt.wantWeightAfter)
			}

			// Verify cluster states report SLO as met.
			for _, cs := range advanced.ClusterStates {
				if !cs.SLOMet {
					t.Errorf("cluster %q: SLOMet = false, want true", cs.ClusterID)
				}
			}
		})
	}
}

func TestSLOGateFail(t *testing.T) {
	tests := []struct {
		name           string
		setupLifecycle v1alpha1.ModelLifecycleSpec
		wantPhase      string
		wantWeight     int
		wantErr        bool
	}{
		{
			name: "SLO gate failure triggers rollback",
			setupLifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "llama-3-70b",
					Version: "v2",
				},
				FleetPoolRef: "production-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:   10,
						WeightIncrement: 20,
						Interval:        "5m",
						SLOGate: &v1alpha1.SLOGate{
							MaxTTFTRegression:    "0%",
							MaxErrorRateIncrease: "0%",
						},
						RollbackOnFailure: true,
					},
				},
				Clusters: &v1alpha1.ClusterOrder{
					Order: []string{"us-east-1"},
				},
			},
			wantPhase:  "RolledBack",
			wantWeight: 0,
			wantErr:    false,
		},
		{
			name: "SLO gate failure without rollback keeps weight unchanged",
			setupLifecycle: v1alpha1.ModelLifecycleSpec{
				Model: v1alpha1.ModelRef{
					Name:    "llama-3-70b",
					Version: "v2",
				},
				FleetPoolRef: "production-pool",
				Strategy: v1alpha1.RolloutStrategy{
					Type: "Canary",
					Canary: &v1alpha1.CanaryConfig{
						InitialWeight:   10,
						WeightIncrement: 20,
						Interval:        "5m",
						SLOGate: &v1alpha1.SLOGate{
							MaxTTFTRegression:    "0%",
							MaxErrorRateIncrease: "0%",
						},
						RollbackOnFailure: false,
					},
				},
			},
			wantPhase:  "Canary",
			wantWeight: 10,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := NewRolloutController()

			// Create the rollout.
			created, err := ctrl.CreateRollout(context.Background(), tt.setupLifecycle)
			if err != nil {
				t.Fatalf("setup: CreateRollout failed: %v", err)
			}

			// Attempt to advance -- SLO gate should fail.
			state, err := ctrl.AdvanceRollout(context.Background(), created.ID)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if state.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", state.Phase, tt.wantPhase)
			}

			if state.CurrentWeight != tt.wantWeight {
				t.Errorf("weight = %d, want %d", state.CurrentWeight, tt.wantWeight)
			}
		})
	}
}
