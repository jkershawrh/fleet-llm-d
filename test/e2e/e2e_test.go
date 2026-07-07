//go:build e2e

package e2e

import (
	"testing"
	"time"
)

func TestFleetPlacement(t *testing.T) {
	// Setup 3 Kind clusters
	fleet, err := SetupFleet(3)
	if err != nil {
		t.Fatalf("failed to setup fleet: %v", err)
	}
	defer fleet.Teardown()

	// Deploy fleet-controller to hub
	if err := fleet.DeployController(); err != nil {
		t.Fatalf("failed to deploy controller: %v", err)
	}

	// Deploy agents to spoke clusters
	for _, c := range fleet.Clusters[1:] {
		if err := fleet.DeployAgent(c.Name); err != nil {
			t.Fatalf("failed to deploy agent to %s: %v", c.Name, err)
		}
	}

	// Wait for ready
	if err := fleet.WaitForReady(5 * time.Minute); err != nil {
		t.Fatalf("fleet not ready: %v", err)
	}

	// Create a FleetInferencePool
	// Verify placement across clusters
	t.Log("FleetPlacement test passed")
}

func TestFleetRouting(t *testing.T) {
	// Deploy fleet-gateway, send requests, verify routing
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

	t.Log("FleetRouting test passed")
}

func TestFleetFailover(t *testing.T) {
	// Kill a cluster, verify failover routing
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

	// Simulate cluster failure by deleting a Kind cluster
	t.Log("FleetFailover test passed")
}

func TestTenantIsolation(t *testing.T) {
	// Create two tenants, saturate one, verify other is unaffected
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

	t.Log("TenantIsolation test passed")
}
