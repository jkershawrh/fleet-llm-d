package collector

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PoolMetrics holds metrics for a single inference pool.
type PoolMetrics struct {
	PoolName       string
	Model          string
	Replicas       int
	QueueDepth     int
	TTFT_P99_Ms    float64
	Throughput_TPS float64
	GPUUtilization float64
	KVCacheHitRate float64
}

// ClusterMetrics aggregates pool-level metrics for a cluster.
type ClusterMetrics struct {
	ClusterID string
	Pools     []PoolMetrics
	Timestamp time.Time
}

// MetricsCollector defines the interface for gathering autoscaling metrics.
type MetricsCollector interface {
	CollectAll(ctx context.Context) ([]ClusterMetrics, error)
	CollectCluster(ctx context.Context, clusterID string) (*ClusterMetrics, error)
}

// InMemoryCollector stores ClusterMetrics in a thread-safe map.
type InMemoryCollector struct {
	mu      sync.RWMutex
	metrics map[string]ClusterMetrics
}

// NewMetricsCollector returns a new MetricsCollector pre-seeded with
// default cluster data so it is usable out of the box.
func NewMetricsCollector() MetricsCollector {
	c := &InMemoryCollector{
		metrics: make(map[string]ClusterMetrics),
	}
	// Seed with a default cluster so CollectAll returns data immediately.
	c.Add(ClusterMetrics{
		ClusterID: "default-cluster",
		Pools: []PoolMetrics{
			{
				PoolName:       "default-pool",
				Model:          "default-model",
				Replicas:       1,
				QueueDepth:     0,
				TTFT_P99_Ms:    50.0,
				Throughput_TPS: 10.0,
				GPUUtilization: 0.50,
				KVCacheHitRate: 0.80,
			},
		},
		Timestamp: time.Now(),
	})
	return c
}

// Add registers (or updates) metrics for a cluster. It is safe for
// concurrent use and is primarily intended for testing.
func (c *InMemoryCollector) Add(metrics ClusterMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics[metrics.ClusterID] = metrics
}

// CollectAll returns all stored cluster metrics.
func (c *InMemoryCollector) CollectAll(ctx context.Context) ([]ClusterMetrics, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ClusterMetrics, 0, len(c.metrics))
	for _, m := range c.metrics {
		result = append(result, m)
	}
	return result, nil
}

// CollectCluster returns metrics for a specific cluster, or an error if
// the cluster is not found.
func (c *InMemoryCollector) CollectCluster(ctx context.Context, clusterID string) (*ClusterMetrics, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.metrics[clusterID]
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterID)
	}
	return &m, nil
}
