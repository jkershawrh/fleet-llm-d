//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestPGClient_Integration runs CRUD operations against a real PostgreSQL
// instance. Requires FLEET_PG_URL to be set (e.g.,
// "postgres://user:pass@localhost:5432/fleet?sslmode=disable").
//
// The schema from deploy/migrations/001_initial_schema.sql must already be
// applied to the target database.
func TestPGClient_Integration(t *testing.T) {
	connStr := os.Getenv("FLEET_PG_URL")
	if connStr == "" {
		t.Skip("FLEET_PG_URL not set — skipping integration test")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	pg := NewPGClientFromDB(db)
	ctx := context.Background()

	if err := pg.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// ---------------------------------------------------------------
	// Clusters
	// ---------------------------------------------------------------
	t.Run("Clusters", func(t *testing.T) {
		cluster := ClusterRecord{
			ID:     "int-test-cluster-001",
			Name:   "integration-test-cluster",
			Region: "us-east-1",
			Labels: map[string]string{"env": "test"},
			Status: "active",
		}
		// Clean up from any previous run.
		_ = pg.DeleteCluster(ctx, cluster.ID)

		if err := pg.CreateCluster(ctx, cluster); err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
		defer pg.DeleteCluster(ctx, cluster.ID)

		got, err := pg.GetCluster(ctx, cluster.ID)
		if err != nil {
			t.Fatalf("GetCluster: %v", err)
		}
		if got.Name != cluster.Name {
			t.Errorf("expected name %q, got %q", cluster.Name, got.Name)
		}
		if got.Labels["env"] != "test" {
			t.Errorf("expected label env=test, got %v", got.Labels)
		}

		list, err := pg.ListClusters(ctx)
		if err != nil {
			t.Fatalf("ListClusters: %v", err)
		}
		found := false
		for _, c := range list {
			if c.ID == cluster.ID {
				found = true
			}
		}
		if !found {
			t.Error("ListClusters: integration test cluster not found")
		}

		cluster.Status = "draining"
		if err := pg.UpdateCluster(ctx, cluster); err != nil {
			t.Fatalf("UpdateCluster: %v", err)
		}
		got, _ = pg.GetCluster(ctx, cluster.ID)
		if got.Status != "draining" {
			t.Errorf("UpdateCluster: expected draining, got %s", got.Status)
		}
	})

	// ---------------------------------------------------------------
	// Tenants + Usage
	// ---------------------------------------------------------------
	t.Run("Tenants", func(t *testing.T) {
		tenant := TenantRecord{
			ID:       "int-test-tenant-001",
			Name:     "integration-test-tenant",
			Priority: 5,
			Quotas:   map[string]interface{}{"max_tokens": float64(100000)},
		}
		_ = pg.DeleteTenant(ctx, tenant.ID)

		if err := pg.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		defer pg.DeleteTenant(ctx, tenant.ID)

		got, err := pg.GetTenant(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("GetTenant: %v", err)
		}
		if got.Priority != 5 {
			t.Errorf("expected priority 5, got %d", got.Priority)
		}

		// Record usage
		now := time.Now()
		usage := TenantUsageRecord{
			TenantID:       tenant.ID,
			Model:          "granite-3.3-2b",
			TokensConsumed: 2000,
			CostUSD:        0.02,
			RequestCount:   5,
			PeriodStart:    now.Add(-1 * time.Hour),
			PeriodEnd:      now,
		}
		if err := pg.RecordUsage(ctx, usage); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}

		records, err := pg.GetUsage(ctx, tenant.ID, now.Add(-2*time.Hour), now.Add(time.Hour))
		if err != nil {
			t.Fatalf("GetUsage: %v", err)
		}
		if len(records) < 1 {
			t.Fatalf("GetUsage: expected at least 1 record, got %d", len(records))
		}
	})

	// ---------------------------------------------------------------
	// Events
	// ---------------------------------------------------------------
	t.Run("Events", func(t *testing.T) {
		event := EventRecord{
			EventType: "integration.test",
			Payload:   map[string]interface{}{"test": true},
			Source:    "integration-test",
		}
		if err := pg.CreateEvent(ctx, event); err != nil {
			t.Fatalf("CreateEvent: %v", err)
		}

		results, err := pg.QueryEvents(ctx, EventFilter{
			EventType: "integration.test",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(results) < 1 {
			t.Fatalf("QueryEvents: expected at least 1 result, got %d", len(results))
		}
	})
}
