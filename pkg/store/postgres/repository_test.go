package postgres

import (
	"context"
	"testing"
	"time"
)

// --------------------------------------------------------------------------
// ClusterRepository tests
// --------------------------------------------------------------------------

func TestClusterRepository_CreateAndGet(t *testing.T) {
	repo := NewInMemoryClusterRepository()
	ctx := context.Background()

	cluster := ClusterRecord{
		ID:     "cluster-1",
		Name:   "us-east-prod",
		Region: "us-east-1",
		Labels: map[string]string{"env": "production"},
		Status: "active",
	}

	if err := repo.Create(ctx, cluster); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.Get(ctx, "cluster-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "us-east-prod" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "us-east-prod")
	}
	if got.Region != "us-east-1" {
		t.Errorf("Get().Region = %q, want %q", got.Region, "us-east-1")
	}
}

func TestClusterRepository_CreateDuplicate(t *testing.T) {
	repo := NewInMemoryClusterRepository()
	ctx := context.Background()

	cluster := ClusterRecord{ID: "dup-1", Name: "test"}
	if err := repo.Create(ctx, cluster); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if err := repo.Create(ctx, cluster); err == nil {
		t.Error("second Create() expected error for duplicate, got nil")
	}
}

func TestClusterRepository_List(t *testing.T) {
	repo := NewInMemoryClusterRepository()
	ctx := context.Background()

	_ = repo.Create(ctx, ClusterRecord{ID: "c1", Name: "one"})
	_ = repo.Create(ctx, ClusterRecord{ID: "c2", Name: "two"})

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Errorf("List() returned %d clusters, want 2", len(list))
	}
}

func TestClusterRepository_Update(t *testing.T) {
	repo := NewInMemoryClusterRepository()
	ctx := context.Background()

	_ = repo.Create(ctx, ClusterRecord{ID: "c1", Name: "before", Status: "pending"})

	if err := repo.Update(ctx, ClusterRecord{ID: "c1", Name: "after", Status: "active"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := repo.Get(ctx, "c1")
	if got.Name != "after" {
		t.Errorf("after Update, Name = %q, want %q", got.Name, "after")
	}
	if got.Status != "active" {
		t.Errorf("after Update, Status = %q, want %q", got.Status, "active")
	}
}

func TestClusterRepository_Delete(t *testing.T) {
	repo := NewInMemoryClusterRepository()
	ctx := context.Background()

	_ = repo.Create(ctx, ClusterRecord{ID: "c1", Name: "doomed"})

	if err := repo.Delete(ctx, "c1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := repo.Get(ctx, "c1"); err == nil {
		t.Error("Get() after Delete expected error, got nil")
	}
}

func TestClusterRepository_GetNotFound(t *testing.T) {
	repo := NewInMemoryClusterRepository()
	if _, err := repo.Get(context.Background(), "no-such"); err == nil {
		t.Error("Get() expected error for missing cluster, got nil")
	}
}

// --------------------------------------------------------------------------
// TenantRepository tests
// --------------------------------------------------------------------------

func TestTenantRepository_CreateAndGet(t *testing.T) {
	repo := NewInMemoryTenantRepository()
	ctx := context.Background()

	tenant := TenantRecord{
		ID:       "tenant-1",
		Name:     "acme-corp",
		Priority: 10,
		Quotas:   map[string]interface{}{"gpu": 100},
	}

	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.Get(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "acme-corp" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "acme-corp")
	}
	if got.Priority != 10 {
		t.Errorf("Get().Priority = %d, want %d", got.Priority, 10)
	}
}

func TestTenantRepository_RecordAndGetUsage(t *testing.T) {
	repo := NewInMemoryTenantRepository()
	ctx := context.Background()

	_ = repo.Create(ctx, TenantRecord{ID: "tenant-1", Name: "acme-corp"})

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	usage := TenantUsageRecord{
		TenantID:       "tenant-1",
		Model:          "llama-3",
		ClusterID:      "cluster-1",
		TokensConsumed: 50000,
		CostUSD:        1.25,
		RequestCount:   100,
		PeriodStart:    start,
		PeriodEnd:      end,
	}

	if err := repo.RecordUsage(ctx, usage); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	// Query window that includes the usage period.
	queryStart := start.Add(-time.Hour)
	queryEnd := end.Add(time.Hour)
	records, err := repo.GetUsage(ctx, "tenant-1", queryStart, queryEnd)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("GetUsage() returned %d records, want 1", len(records))
	}
	if records[0].TokensConsumed != 50000 {
		t.Errorf("TokensConsumed = %d, want 50000", records[0].TokensConsumed)
	}
}

func TestTenantRepository_GetUsageFiltersByTenant(t *testing.T) {
	repo := NewInMemoryTenantRepository()
	ctx := context.Background()

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	_ = repo.RecordUsage(ctx, TenantUsageRecord{
		TenantID: "tenant-1", Model: "llama-3", PeriodStart: start, PeriodEnd: end,
	})
	_ = repo.RecordUsage(ctx, TenantUsageRecord{
		TenantID: "tenant-2", Model: "llama-3", PeriodStart: start, PeriodEnd: end,
	})

	queryStart := start.Add(-time.Hour)
	queryEnd := end.Add(time.Hour)
	records, err := repo.GetUsage(ctx, "tenant-1", queryStart, queryEnd)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if len(records) != 1 {
		t.Errorf("GetUsage() returned %d records for tenant-1, want 1", len(records))
	}
}

// --------------------------------------------------------------------------
// EventRepository tests
// --------------------------------------------------------------------------

func TestEventRepository_CreateAndQuery(t *testing.T) {
	repo := NewInMemoryEventRepository()
	ctx := context.Background()

	event := EventRecord{
		EventType: "model.deployed",
		Payload:   map[string]interface{}{"model": "llama-3"},
		Source:    "fleet-controller",
		CreatedAt: time.Now(),
	}

	if err := repo.Create(ctx, event); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	results, err := repo.Query(ctx, EventFilter{EventType: "model.deployed"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Query() returned %d events, want 1", len(results))
	}
	if results[0].Source != "fleet-controller" {
		t.Errorf("Source = %q, want %q", results[0].Source, "fleet-controller")
	}
}

func TestEventRepository_QueryWithFilter(t *testing.T) {
	repo := NewInMemoryEventRepository()
	ctx := context.Background()

	now := time.Now()
	_ = repo.Create(ctx, EventRecord{
		EventType: "model.deployed", Source: "ctrl-1", CreatedAt: now,
	})
	_ = repo.Create(ctx, EventRecord{
		EventType: "model.scaled", Source: "ctrl-1", CreatedAt: now,
	})
	_ = repo.Create(ctx, EventRecord{
		EventType: "model.deployed", Source: "ctrl-2", CreatedAt: now,
	})

	// Filter by type only.
	results, _ := repo.Query(ctx, EventFilter{EventType: "model.deployed"})
	if len(results) != 2 {
		t.Errorf("Query(type=model.deployed) returned %d, want 2", len(results))
	}

	// Filter by source only.
	results, _ = repo.Query(ctx, EventFilter{Source: "ctrl-1"})
	if len(results) != 2 {
		t.Errorf("Query(source=ctrl-1) returned %d, want 2", len(results))
	}

	// Filter by both type and source.
	results, _ = repo.Query(ctx, EventFilter{EventType: "model.deployed", Source: "ctrl-1"})
	if len(results) != 1 {
		t.Errorf("Query(type+source) returned %d, want 1", len(results))
	}

	// Filter with limit.
	results, _ = repo.Query(ctx, EventFilter{Limit: 1})
	if len(results) != 1 {
		t.Errorf("Query(limit=1) returned %d, want 1", len(results))
	}
}

func TestEventRepository_QueryWithTimeRange(t *testing.T) {
	repo := NewInMemoryEventRepository()
	ctx := context.Background()

	t1 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 7, 1, 14, 0, 0, 0, time.UTC)

	_ = repo.Create(ctx, EventRecord{EventType: "a", CreatedAt: t1})
	_ = repo.Create(ctx, EventRecord{EventType: "b", CreatedAt: t2})
	_ = repo.Create(ctx, EventRecord{EventType: "c", CreatedAt: t3})

	since := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	until := time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC)
	results, _ := repo.Query(ctx, EventFilter{Since: &since, Until: &until})
	if len(results) != 1 {
		t.Errorf("Query(time range) returned %d, want 1 (only event at 12:00)", len(results))
	}
}
