package postgres

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Interface contract tests — run against the in-memory implementations to
// verify the repository contracts that PGClient must also satisfy.
// ---------------------------------------------------------------------------

// TestInterfaceContract_ClusterRepository verifies ClusterRepository operations
// match the behaviour PGClient must replicate.
func TestInterfaceContract_ClusterRepository(t *testing.T) {
	var repo ClusterRepository = NewInMemoryClusterRepository()
	ctx := context.Background()

	// Create
	c := ClusterRecord{
		ID:     "cluster-001",
		Name:   "us-east-1a",
		Region: "us-east-1",
		Labels: map[string]string{"env": "prod"},
		Status: "active",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	got, err := repo.Get(ctx, "cluster-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "us-east-1a" || got.Region != "us-east-1" {
		t.Errorf("Get returned unexpected record: %+v", got)
	}

	// List
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: expected 1, got %d", len(list))
	}

	// Update
	c.Status = "draining"
	if err := repo.Update(ctx, c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = repo.Get(ctx, "cluster-001")
	if got.Status != "draining" {
		t.Errorf("Update: expected status draining, got %s", got.Status)
	}

	// Delete
	if err := repo.Delete(ctx, "cluster-001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "cluster-001"); err == nil {
		t.Error("expected error after Delete, got nil")
	}
}

// TestInterfaceContract_FleetPoolRepository verifies FleetPoolRepository.
func TestInterfaceContract_FleetPoolRepository(t *testing.T) {
	var repo FleetPoolRepository = NewInMemoryFleetPoolRepository()
	ctx := context.Background()

	p := FleetPoolRecord{
		ID:        "pool-001",
		Name:      "granite-pool",
		ModelName: "granite-3.3-2b",
		Status:    "active",
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "pool-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ModelName != "granite-3.3-2b" {
		t.Errorf("Get: unexpected model name %s", got.ModelName)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: expected 1, got %d", len(list))
	}

	p.Status = "draining"
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := repo.Delete(ctx, "pool-001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "pool-001"); err == nil {
		t.Error("expected error after Delete, got nil")
	}
}

// TestInterfaceContract_TenantRepository verifies TenantRepository including
// usage recording.
func TestInterfaceContract_TenantRepository(t *testing.T) {
	var repo TenantRepository = NewInMemoryTenantRepository()
	ctx := context.Background()

	tenant := TenantRecord{
		ID:       "tenant-001",
		Name:     "acme-corp",
		Priority: 10,
		Quotas:   map[string]interface{}{"max_tokens": 1000000},
	}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "tenant-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Priority != 10 {
		t.Errorf("Get: unexpected priority %d", got.Priority)
	}

	// Record usage
	now := time.Now()
	usage := TenantUsageRecord{
		TenantID:       "tenant-001",
		Model:          "granite-3.3-2b",
		TokensConsumed: 5000,
		CostUSD:        0.05,
		RequestCount:   10,
		PeriodStart:    now.Add(-1 * time.Hour),
		PeriodEnd:      now,
	}
	if err := repo.RecordUsage(ctx, usage); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	// Get usage
	records, err := repo.GetUsage(ctx, "tenant-001", now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("GetUsage: expected 1, got %d", len(records))
	}
	if records[0].TokensConsumed != 5000 {
		t.Errorf("GetUsage: unexpected tokens %d", records[0].TokensConsumed)
	}

	// Update
	tenant.Priority = 20
	if err := repo.Update(ctx, tenant); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Delete
	if err := repo.Delete(ctx, "tenant-001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// TestInterfaceContract_RolloutRepository verifies RolloutRepository.
func TestInterfaceContract_RolloutRepository(t *testing.T) {
	var repo RolloutRepository = NewInMemoryRolloutRepository()
	ctx := context.Background()

	r := RolloutRecord{
		ID:            "rollout-001",
		PoolID:        "pool-001",
		ModelVersion:  "v2.0",
		Strategy:      map[string]interface{}{"type": "canary"},
		Status:        "pending",
		CurrentWeight: 0,
	}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "rollout-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ModelVersion != "v2.0" {
		t.Errorf("Get: unexpected model version %s", got.ModelVersion)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: expected 1, got %d", len(list))
	}

	r.Status = "active"
	r.CurrentWeight = 50
	if err := repo.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = repo.Get(ctx, "rollout-001")
	if got.Status != "active" || got.CurrentWeight != 50 {
		t.Errorf("Update: expected active/50, got %s/%d", got.Status, got.CurrentWeight)
	}
}

// TestInterfaceContract_EventRepository verifies EventRepository.
func TestInterfaceContract_EventRepository(t *testing.T) {
	var repo EventRepository = NewInMemoryEventRepository()
	ctx := context.Background()

	e := EventRecord{
		EventType: "model.deployed",
		Payload:   map[string]interface{}{"model": "granite"},
		Source:    "fleet-controller",
	}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, err := repo.Query(ctx, EventFilter{EventType: "model.deployed"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Query: expected 1, got %d", len(results))
	}
	if results[0].Source != "fleet-controller" {
		t.Errorf("Query: unexpected source %s", results[0].Source)
	}

	// Query with no match
	results, err = repo.Query(ctx, EventFilter{EventType: "nonexistent"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Query: expected 0 for nonexistent type, got %d", len(results))
	}
}

// TestPGClientFromDB verifies the constructor works with a nil DB (smoke test
// for the API — actual queries would panic, but the constructor should not).
func TestPGClientFromDB(t *testing.T) {
	pg := NewPGClientFromDB(nil)
	if pg == nil {
		t.Fatal("NewPGClientFromDB returned nil")
	}
	if pg.db != nil {
		t.Fatal("expected nil db")
	}
}
