package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PoolMetrics holds metrics for a single inference pool.
type PoolMetrics struct {
	PoolName              string
	Model                 string
	Replicas              int
	QueueDepth            int
	TTFT_P50_Ms           float64
	TTFT_P99_Ms           float64
	Throughput_TPS        float64
	GPUUtilization        float64
	KVCacheHitRate        float64
	CPUUtilization        float64
	InferenceLatencyP99Ms float64
}

// ClusterMetrics aggregates pool-level metrics for a cluster.
type ClusterMetrics struct {
	ClusterID string
	Pools     []PoolMetrics
	Timestamp time.Time
}

// MetricsCollector defines the interface for gathering autoscaling metrics.
type MetricsCollector interface {
	Add(metrics ClusterMetrics)
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

// PrometheusCollector scrapes metrics from a Prometheus endpoint.
// It implements MetricsCollector so it can be used as a drop-in replacement
// for InMemoryCollector. Real Prometheus format parsing is not yet
// implemented; for now metrics are populated via Add() or ScrapeOnce().
type PrometheusCollector struct {
	mu        sync.RWMutex
	metrics   map[string]ClusterMetrics
	scrapeURL string
	http      *http.Client
}

// NewPrometheusCollector returns a PrometheusCollector targeting the given
// Prometheus scrape URL.
func NewPrometheusCollector(scrapeURL string) *PrometheusCollector {
	return &PrometheusCollector{
		metrics:   make(map[string]ClusterMetrics),
		scrapeURL: scrapeURL,
		http:      &http.Client{Timeout: 5 * time.Second},
	}
}

// Add registers (or updates) metrics for a cluster. It is safe for
// concurrent use and is primarily intended for testing.
func (c *PrometheusCollector) Add(metrics ClusterMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics[metrics.ClusterID] = metrics
}

// CollectAll returns all stored cluster metrics.
func (c *PrometheusCollector) CollectAll(ctx context.Context) ([]ClusterMetrics, error) {
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
func (c *PrometheusCollector) CollectCluster(ctx context.Context, clusterID string) (*ClusterMetrics, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.metrics[clusterID]
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterID)
	}
	return &m, nil
}

// ScrapeOnce fetches metrics from the configured Prometheus endpoint.
// Real Prometheus format parsing is not yet implemented; this method
// currently only exercises the HTTP path and returns the raw response
// body length for diagnostic purposes.
func (c *PrometheusCollector) ScrapeOnce(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.scrapeURL, nil)
	if err != nil {
		return 0, fmt.Errorf("building request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("scraping %s: %w", c.scrapeURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, fmt.Errorf("reading response: %w", err)
	}

	pool := parsePrometheusText(string(body))
	c.Add(ClusterMetrics{
		ClusterID: "scraped",
		Pools:     []PoolMetrics{pool},
		Timestamp: time.Now().UTC(),
	})
	return len(body), nil
}

func parsePrometheusText(body string) PoolMetrics {
	var pm PoolMetrics
	pm.PoolName = "scraped"
	var gpuTotal, gpuCount float64

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := strings.SplitN(parts[0], "{", 2)[0]
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}

		switch {
		case strings.Contains(name, "throughput") && !strings.HasSuffix(name, "_total"):
			pm.Throughput_TPS += val
		case strings.Contains(name, "ttft") && strings.Contains(name, "p50"):
			if val > pm.TTFT_P50_Ms {
				pm.TTFT_P50_Ms = val
			}
		case strings.Contains(name, "ttft") && strings.Contains(name, "p99"):
			if val > pm.TTFT_P99_Ms {
				pm.TTFT_P99_Ms = val
			}
		case strings.Contains(name, "queue_depth"):
			pm.QueueDepth += int(val)
		case strings.Contains(name, "gpu_utilization") || name == "habana_device_utilization":
			gpuTotal += val
			gpuCount++
		case strings.Contains(name, "kv_cache_hit_rate"):
			pm.KVCacheHitRate = val
		case strings.Contains(name, "cpu_utilization"):
			pm.CPUUtilization = val
		}
	}

	if gpuCount > 0 {
		pm.GPUUtilization = gpuTotal / gpuCount
	}
	return pm
}
