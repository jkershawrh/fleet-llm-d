package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

type transientGetRepository struct {
	postgres.ClusterRepository
	failNext bool
}

func (r *transientGetRepository) Get(ctx context.Context, id string) (*postgres.ClusterRecord, error) {
	if r.failNext {
		r.failNext = false
		return nil, errors.New("transient repository failure")
	}
	return r.ClusterRepository.Get(ctx, id)
}

func TestSWIMSyncRetriesPhaseAfterRepositoryFailure(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"cluster-1"},"status":{"phase":"Unreachable"}}]}`))
	}))
	defer api.Close()

	base := postgres.NewInMemoryClusterRepository()
	if err := base.Create(context.Background(), postgres.ClusterRecord{ID: "cluster-1", Status: "Running"}); err != nil {
		t.Fatal(err)
	}
	repo := &transientGetRepository{ClusterRepository: base, failNext: true}
	adapter := NewSWIMSyncAdapter(api.URL, "", repo)

	if updated, err := adapter.Sync(context.Background()); err != nil || updated != 0 {
		t.Fatalf("first sync = (%d, %v), want transient skip", updated, err)
	}
	if _, cached := adapter.lastPhases["cluster-1"]; cached {
		t.Fatal("phase was cached before the authoritative repository write")
	}
	if updated, err := adapter.Sync(context.Background()); err != nil || updated != 1 {
		t.Fatalf("retry sync = (%d, %v), want one update", updated, err)
	}
	record, err := base.Get(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "Degraded" {
		t.Fatalf("status = %q, want Degraded", record.Status)
	}
}
