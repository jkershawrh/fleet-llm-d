package routing

import (
	"context"
	"testing"
	"time"
)

func TestBindAndLookup(t *testing.T) {
	table := NewSessionAffinityTable(5 * time.Minute)
	table.Bind("sess-1", "cluster-a")

	clusterID, found := table.Lookup("sess-1")
	if !found {
		t.Fatal("expected binding to be found")
	}
	if clusterID != "cluster-a" {
		t.Fatalf("expected cluster-a, got %s", clusterID)
	}
}

func TestLookupNotFound(t *testing.T) {
	table := NewSessionAffinityTable(5 * time.Minute)
	_, found := table.Lookup("nonexistent")
	if found {
		t.Fatal("expected binding not found")
	}
}

func TestLookupExpired(t *testing.T) {
	table := NewSessionAffinityTable(1 * time.Millisecond)
	table.Bind("sess-1", "cluster-a")
	time.Sleep(5 * time.Millisecond)

	_, found := table.Lookup("sess-1")
	if found {
		t.Fatal("expected expired binding not found")
	}
}

func TestUnbind(t *testing.T) {
	table := NewSessionAffinityTable(5 * time.Minute)
	table.Bind("sess-1", "cluster-a")
	table.Unbind("sess-1")

	_, found := table.Lookup("sess-1")
	if found {
		t.Fatal("expected unbound session not found")
	}
}

func TestUnbindCluster(t *testing.T) {
	table := NewSessionAffinityTable(5 * time.Minute)
	table.Bind("sess-1", "cluster-a")
	table.Bind("sess-2", "cluster-a")
	table.Bind("sess-3", "cluster-b")

	table.UnbindCluster("cluster-a")

	if _, found := table.Lookup("sess-1"); found {
		t.Fatal("sess-1 should be unbound")
	}
	if _, found := table.Lookup("sess-2"); found {
		t.Fatal("sess-2 should be unbound")
	}
	if _, found := table.Lookup("sess-3"); !found {
		t.Fatal("sess-3 should still be bound")
	}
}

func TestReaper(t *testing.T) {
	table := NewSessionAffinityTable(1 * time.Millisecond)
	table.Bind("sess-1", "cluster-a")
	table.Bind("sess-2", "cluster-b")

	time.Sleep(5 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	table.StartReaper(ctx, 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	cancel()

	if table.Len() != 0 {
		t.Fatalf("expected 0 bindings after reap, got %d", table.Len())
	}
}

func TestBindUpdatesExisting(t *testing.T) {
	table := NewSessionAffinityTable(5 * time.Minute)
	table.Bind("sess-1", "cluster-a")
	table.Bind("sess-1", "cluster-b")

	clusterID, found := table.Lookup("sess-1")
	if !found {
		t.Fatal("expected binding to be found")
	}
	if clusterID != "cluster-b" {
		t.Fatalf("expected cluster-b after rebind, got %s", clusterID)
	}
}
