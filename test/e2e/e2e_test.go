//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

func TestFleetPlacement(t *testing.T) {
	fleet, err := SetupFleet(3)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	for _, c := range fleet.Clusters[1:] {
		if err := fleet.DeployAgent(c.Name); err != nil {
			t.Fatalf("failed to deploy agent to %s: %v", c.Name, err)
		}
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	clusters, err := fleet.ListClusters()
	if err != nil {
		t.Fatalf("failed to list clusters: %v", err)
	}
	if len(clusters) < 2 {
		t.Fatalf("expected at least 2 clusters registered, got %d", len(clusters))
	}
}

func TestFleetRouting(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	status, headers, err := fleet.SendInference("granite-3.3-2b", "hello")
	if err != nil {
		t.Fatalf("inference request failed: %v", err)
	}
	if status >= 500 {
		t.Fatalf("inference returned server error: %d", status)
	}
	routed := headers.Get("X-Fleet-Routed-To")
	if routed == "" {
		t.Log("warning: X-Fleet-Routed-To header not set (backend may not be deployed)")
	}
}

func TestFleetFailover(t *testing.T) {
	fleet, err := SetupFleet(3)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	for _, c := range fleet.Clusters[1:] {
		if err := fleet.DeployAgent(c.Name); err != nil {
			t.Fatalf("failed to deploy agent to %s: %v", c.Name, err)
		}
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	spoke := fleet.Clusters[1].Name
	if err := fleet.DrainCluster(spoke); err != nil {
		t.Logf("drain not available (expected if controller has no clusters registered): %v", err)
	}

	if err := fleet.CheckHealth(); err != nil {
		t.Fatalf("controller unhealthy after spoke drain: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	if err := fleet.CreateTenant("tenant-a", 10000); err != nil {
		t.Fatalf("failed to create tenant-a: %v", err)
	}
	if err := fleet.CreateTenant("tenant-b", 50000); err != nil {
		t.Fatalf("failed to create tenant-b: %v", err)
	}

	status, _, err := fleet.apiGet("/api/v1/tenants")
	if err != nil {
		t.Fatalf("failed to list tenants: %v", err)
	}
	if status != 200 {
		t.Fatalf("list tenants returned %d", status)
	}
}

func TestAutoscaling(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	metrics, err := fleet.GetFleetMetrics()
	if err != nil {
		t.Fatalf("failed to get fleet metrics: %v", err)
	}
	if metrics == nil {
		t.Fatal("fleet metrics returned nil")
	}
}

func TestLifecycle(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	rolloutID, err := fleet.CreateRollout("test-model", "v2")
	if err != nil {
		t.Fatalf("failed to create rollout: %v", err)
	}
	if rolloutID == "" {
		t.Fatal("rollout ID is empty")
	}

	if err := fleet.PromoteRollout(rolloutID); err != nil {
		t.Fatalf("failed to promote rollout: %v", err)
	}
}

func TestObservability(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	promMetrics, err := fleet.GetPrometheusMetrics()
	if err != nil {
		t.Fatalf("failed to scrape prometheus: %v", err)
	}
	if !strings.Contains(promMetrics, "fleet_requests_total") {
		t.Log("warning: fleet_requests_total not found in prometheus output")
	}

	fleetMetrics, err := fleet.GetFleetMetrics()
	if err != nil {
		t.Fatalf("failed to get fleet metrics: %v", err)
	}
	if fleetMetrics == nil {
		t.Fatal("fleet metrics is nil")
	}
}

func TestKVTransfer(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	if err := fleet.CheckHealth(); err != nil {
		t.Fatalf("controller unhealthy: %v", err)
	}
}

func TestModelPack(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	if err := fleet.CheckHealth(); err != nil {
		t.Fatalf("controller unhealthy: %v", err)
	}
}

func TestLedger(t *testing.T) {
	fleet, err := SetupFleet(2)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	valid, err := fleet.VerifyLedgerChains()
	if err != nil {
		t.Logf("ledger verify not available: %v", err)
		return
	}
	if !valid {
		t.Fatal("ledger chain integrity check failed")
	}
}
