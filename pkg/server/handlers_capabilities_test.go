package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestCapabilityStatusRequiresTwoHealthyProvidersForHA(t *testing.T) {
	fc := newTestFleetController(t)
	for _, id := range []string{"oberon-cpu", "arena-xeon6", "brutus-h100"} {
		if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{ID: id, Name: id, Status: "Running"}); err != nil {
			t.Fatal(err)
		}
	}
	health := make(map[string]bool)
	for _, site := range fc.BuildClusterHealth(context.Background()) {
		health[site.ClusterID] = site.Healthy
	}
	if got := capabilityFor(defaultCPUModel, []string{"oberon-cpu", "arena-xeon6"}, health).Status; got != "healthy" {
		t.Fatalf("CPU status = %q", got)
	}
	if got := capabilityFor(defaultGPUModel, []string{"brutus-h100"}, health).Status; got != "degraded/non-HA" {
		t.Fatalf("GPU status = %q", got)
	}
}

func TestProviderCapabilityDetailsExposeNormalizedRoutingState(t *testing.T) {
	fc := newTestFleetController(t)
	now := time.Now().UTC()
	if err := fc.ClusterRepo.Create(context.Background(), postgres.ClusterRecord{
		ID: "oberon-cpu", Name: "oberon", Region: "central", Status: "Running", UpdatedAt: now,
		Labels: map[string]string{
			"fleet.llm-d.ai/egress-address":   "https://oberon.example",
			"fleet.llm-d.ai/metrics-endpoint": "https://metrics.oberon.example",
			"topology.kubernetes.io/zone":     "zone-a",
		},
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/capabilities", nil)
	details := fc.providerCapabilityDetails(req, defaultCPUModel, []string{"oberon-cpu"}, map[string]bool{"oberon-cpu": true})
	if len(details) != 1 || details[0].Status != "healthy" || details[0].FailureDomain != "zone-a" || details[0].PhysicalModel != defaultCPUModel {
		t.Fatalf("details = %#v", details)
	}
}
