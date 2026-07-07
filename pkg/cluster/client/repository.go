package client

import (
	"context"
	"fmt"

	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// repositoryClusterClient persists cluster registrations through a
// ClusterRepository while still satisfying the MultiClusterClient contract.
type repositoryClusterClient struct {
	repo postgres.ClusterRepository
}

// NewRepositoryClusterClient creates a MultiClusterClient backed by the given
// repository.
func NewRepositoryClusterClient(repo postgres.ClusterRepository) MultiClusterClient {
	return &repositoryClusterClient{repo: repo}
}

func (c *repositoryClusterClient) RegisterCluster(ctx context.Context, cluster ClusterRegistration) error {
	normalized, err := NormalizeClusterRegistration(cluster)
	if err != nil {
		return err
	}
	return c.repo.Create(ctx, postgres.ClusterRecord{
		ID:     normalized.ID,
		Name:   normalized.Name,
		Region: normalized.Region,
		Labels: normalized.Labels,
		Status: "registered",
	})
}

func (c *repositoryClusterClient) DeregisterCluster(ctx context.Context, clusterID string) error {
	if clusterID == "" {
		return fmt.Errorf("cluster ID must not be empty")
	}
	return c.repo.Delete(ctx, clusterID)
}

func (c *repositoryClusterClient) ListClusters(ctx context.Context) ([]solver.ClusterInfo, error) {
	records, err := c.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	clusters := make([]solver.ClusterInfo, 0, len(records))
	for _, rec := range records {
		clusters = append(clusters, solver.ClusterInfo{
			ID:     rec.ID,
			Name:   rec.Name,
			Region: rec.Region,
			Labels: rec.Labels,
		})
	}
	return clusters, nil
}

func (c *repositoryClusterClient) GetClusterClient(ctx context.Context, clusterID string) (interface{}, error) {
	if clusterID == "" {
		return nil, fmt.Errorf("cluster ID must not be empty")
	}
	if _, err := c.repo.Get(ctx, clusterID); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("kubernetes client not configured for cluster %q", clusterID)
}

var _ MultiClusterClient = (*repositoryClusterClient)(nil)
