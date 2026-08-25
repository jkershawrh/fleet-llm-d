package server

import (
	"context"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestCapabilityStatusRequiresTwoHealthyProvidersForHA(t *testing.T) {
	fc := newTestFleetController(t)
	for _, id := range []string{"oberon", "arena", "brutus"} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: id, Name: id, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	health := make(map[string]bool)
	for _, site := range fc.BuildClusterHealth(context.Background()) {
		health[site.ClusterID] = site.Healthy
	}
	if got := capabilityFor(defaultCPUModel, []string{"oberon", "arena"}, health).Status; got != "healthy" {
		t.Fatalf("CPU status = %q", got)
	}
	if got := capabilityFor(defaultGPUModel, []string{"brutus"}, health).Status; got != "degraded/non-HA" {
		t.Fatalf("GPU status = %q", got)
	}
}
