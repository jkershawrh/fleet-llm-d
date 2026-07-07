package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

// ClusterRegistration holds the details needed to register a cluster with
// the multi-cluster control plane.
type ClusterRegistration struct {
	ID             string
	Name           string
	Region         string
	KubeconfigPath string
	Labels         map[string]string
}

// MultiClusterClient manages the lifecycle of registered clusters and
// provides access to per-cluster API clients.
type MultiClusterClient interface {
	RegisterCluster(ctx context.Context, cluster ClusterRegistration) error
	DeregisterCluster(ctx context.Context, clusterID string) error
	ListClusters(ctx context.Context) ([]solver.ClusterInfo, error)
	GetClusterClient(ctx context.Context, clusterID string) (interface{}, error)
}

// clusterRegistry is the shared, package-level registry used by all
// defaultMultiClusterClient instances so that clusters registered through
// one client instance are visible from another.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]ClusterRegistration)
)

// defaultMultiClusterClient is the built-in implementation of
// MultiClusterClient.
type defaultMultiClusterClient struct{}

// NewMultiClusterClient returns a new MultiClusterClient using the default
// implementation.
func NewMultiClusterClient() MultiClusterClient {
	return &defaultMultiClusterClient{}
}

// RegisterCluster stores a ClusterRegistration in the shared registry.
func (c *defaultMultiClusterClient) RegisterCluster(ctx context.Context, cluster ClusterRegistration) error {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[cluster.ID] = cluster
	return nil
}

// DeregisterCluster removes a cluster from the shared registry.
func (c *defaultMultiClusterClient) DeregisterCluster(ctx context.Context, clusterID string) error {
	registryMu.Lock()
	defer registryMu.Unlock()

	if _, ok := registry[clusterID]; !ok {
		return fmt.Errorf("cluster %q not found", clusterID)
	}
	delete(registry, clusterID)
	return nil
}

// ListClusters returns all registered clusters as ClusterInfo values.
func (c *defaultMultiClusterClient) ListClusters(ctx context.Context) ([]solver.ClusterInfo, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	clusters := make([]solver.ClusterInfo, 0, len(registry))
	for _, reg := range registry {
		clusters = append(clusters, solver.ClusterInfo{
			ID:     reg.ID,
			Name:   reg.Name,
			Region: reg.Region,
			Labels: reg.Labels,
		})
	}
	return clusters, nil
}

// GetClusterClient returns the Kubernetes client for the given cluster.
// Currently returns an error because no real K8s client is configured.
func (c *defaultMultiClusterClient) GetClusterClient(ctx context.Context, clusterID string) (interface{}, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	if _, ok := registry[clusterID]; !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterID)
	}
	return nil, fmt.Errorf("kubernetes client not configured for cluster %q", clusterID)
}
