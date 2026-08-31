package client

import (
	"context"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func TestRepositoryClusterClient_PersistsRegistration(t *testing.T) {
	repo := postgres.NewInMemoryClusterRepository()
	client := NewRepositoryClusterClient(repo)

	err := client.RegisterCluster(context.Background(), ClusterRegistration{
		Name:   "EU West Production",
		Region: "eu-west-1",
		Labels: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("RegisterCluster() unexpected error: %v", err)
	}

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters() unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0].ID != "eu-west-production" {
		t.Fatalf("expected stable generated ID, got %q", clusters[0].ID)
	}

	record, err := repo.Get(context.Background(), "eu-west-production")
	if err != nil {
		t.Fatalf("repo.Get() unexpected error: %v", err)
	}
	if record.Region != "eu-west-1" {
		t.Fatalf("expected persisted region eu-west-1, got %q", record.Region)
	}
}
