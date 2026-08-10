package server

import (
	"context"
	"testing"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestRewireRepositoriesReplacesAllPersistenceConsumers(t *testing.T) {
	ctx := context.Background()
	fc := NewFleetController("", "", "", "", "")
	oldClusterRepo := fc.ClusterRepo
	newClusterRepo := postgres.NewInMemoryClusterRepository()
	newPoolRepo := postgres.NewInMemoryFleetPoolRepository()
	newTenantRepo := postgres.NewInMemoryTenantRepository()
	newRolloutRepo := postgres.NewInMemoryRolloutRepository()

	fc.rewireRepositories(newClusterRepo, newPoolRepo, newTenantRepo, newRolloutRepo)
	if err := fc.ClusterClient.RegisterCluster(ctx, client.ClusterRegistration{ID: "cluster-1", Name: "cluster-1", Region: "us-east"}); err != nil {
		t.Fatal(err)
	}
	if _, err := newClusterRepo.Get(ctx, "cluster-1"); err != nil {
		t.Fatalf("rewired cluster client did not use the new repository: %v", err)
	}
	if _, err := oldClusterRepo.Get(ctx, "cluster-1"); err == nil {
		t.Fatal("rewired cluster client still wrote to the constructor repository")
	}

	pool := v1alpha1.FleetInferencePoolSpec{
		Model:     v1alpha1.ModelSpec{Name: "model-a", Source: "hf://model-a"},
		Placement: v1alpha1.PlacementRef{PolicyRef: v1alpha1.PolicyReference{Name: "default"}, MaxClusters: 1},
		Serving: v1alpha1.ServingSpec{InferencePoolTemplate: v1alpha1.InferencePoolTemplate{
			Spec: v1alpha1.InferencePoolTemplateSpec{TargetPorts: []int{8000}},
		}},
	}
	if err := fc.Reconciler.ReconcilePool(ctx, pool); err != nil {
		t.Fatal(err)
	}
	persisted, err := newPoolRepo.Get(ctx, "model-a")
	if err != nil {
		t.Fatalf("reconciler callback did not use the rewired pool repository: %v", err)
	}
	if len(persisted.DesiredClusters) != 1 || persisted.DesiredClusters[0] != "cluster-1" {
		t.Fatalf("desired clusters were not persisted: %#v", persisted.DesiredClusters)
	}
	if len(persisted.TargetPorts) != 1 || persisted.TargetPorts[0] != 8000 {
		t.Fatalf("target ports were not persisted: %#v", persisted.TargetPorts)
	}
}
