package metrics

import (
	"context"
	"fmt"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
)

// ClusterMetricsSummary contains aggregated metrics for a single cluster.
type ClusterMetricsSummary struct {
	ClusterID  string
	GPUs       int
	Models     int
	Throughput float64
	AvgTTFT_Ms float64
}

// FleetMetrics contains federated metrics across all clusters.
type FleetMetrics struct {
	TotalGPUs       int
	ActiveModels    int
	TotalThroughput float64
	AvgTTFT_Ms      float64
	Clusters        []ClusterMetricsSummary
}

// ModelMetrics contains metrics for a specific model across clusters.
type ModelMetrics struct {
	Model        string
	Clusters     []string
	Throughput   float64
	TTFT_P50_Ms  float64
	TTFT_P95_Ms  float64
	TTFT_P99_Ms  float64
	CacheHitRate float64
}

// TenantMetrics contains usage metrics for a specific tenant.
type TenantMetrics struct {
	TenantID       string
	TokensConsumed int64
	Cost           string
	AvgLatencyMs   int
}

// MetricsFederator federates metrics across clusters.
type MetricsFederator interface {
	FederateMetrics(ctx context.Context, clusters []string) (*FleetMetrics, error)
	GetModelMetrics(ctx context.Context, model string) (*ModelMetrics, error)
	GetTenantMetrics(ctx context.Context, tenantID string) (*TenantMetrics, error)
}

// InMemoryMetricsFederator stores per-cluster metrics for federation.
type InMemoryMetricsFederator struct {
	clusterMetrics map[string]ClusterMetricsSummary
	modelMetrics   map[string]*ModelMetrics
	tenantMetrics  map[string]*TenantMetrics
	collector      collector.MetricsCollector
}

// NewMetricsFederator returns a new MetricsFederator instance.
// If a collector is provided, FederateMetrics builds from live collector data.
// Otherwise it uses static seed data for backward compatibility.
func NewMetricsFederator(col ...collector.MetricsCollector) MetricsFederator {
	f := &InMemoryMetricsFederator{
		clusterMetrics: make(map[string]ClusterMetricsSummary),
		modelMetrics:   make(map[string]*ModelMetrics),
		tenantMetrics:  make(map[string]*TenantMetrics),
	}
	if len(col) > 0 && col[0] != nil {
		f.collector = col[0]
	}
	return f
}

func (f *InMemoryMetricsFederator) FederateMetrics(ctx context.Context, clusters []string) (*FleetMetrics, error) {
	if f.collector != nil {
		return f.federateFromCollector(ctx, clusters)
	}
	return f.federateFromStatic(ctx, clusters)
}

func (f *InMemoryMetricsFederator) federateFromCollector(ctx context.Context, clusters []string) (*FleetMetrics, error) {
	allMetrics, err := f.collector.CollectAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("collecting metrics: %w", err)
	}

	byCluster := make(map[string]collector.ClusterMetrics, len(allMetrics))
	for _, cm := range allMetrics {
		byCluster[cm.ClusterID] = cm
	}

	result := &FleetMetrics{}
	var totalTTFT float64

	for _, clusterID := range clusters {
		cm, ok := byCluster[clusterID]
		if !ok {
			result.Clusters = append(result.Clusters, ClusterMetricsSummary{ClusterID: clusterID})
			continue
		}

		var throughput, ttft float64
		models := make(map[string]bool)
		for _, pool := range cm.Pools {
			throughput += pool.Throughput_TPS
			ttft += pool.TTFT_P50_Ms
			if pool.Model != "" {
				models[pool.Model] = true
			} else if pool.PoolName != "" {
				models[pool.PoolName] = true
			}
		}
		if len(cm.Pools) > 0 {
			ttft /= float64(len(cm.Pools))
		}

		summary := ClusterMetricsSummary{
			ClusterID:  clusterID,
			Models:     len(models),
			Throughput: throughput,
			AvgTTFT_Ms: ttft,
		}
		result.TotalThroughput += throughput
		result.ActiveModels += len(models)
		totalTTFT += ttft
		result.Clusters = append(result.Clusters, summary)
	}

	if len(clusters) > 0 {
		result.AvgTTFT_Ms = totalTTFT / float64(len(clusters))
	}

	return result, nil
}

func (f *InMemoryMetricsFederator) federateFromStatic(ctx context.Context, clusters []string) (*FleetMetrics, error) {
	result := &FleetMetrics{}
	var totalTTFT float64

	for _, clusterID := range clusters {
		cm, ok := f.clusterMetrics[clusterID]
		if !ok {
			result.Clusters = append(result.Clusters, ClusterMetricsSummary{ClusterID: clusterID})
			continue
		}
		result.TotalGPUs += cm.GPUs
		result.TotalThroughput += cm.Throughput
		result.ActiveModels += cm.Models
		totalTTFT += cm.AvgTTFT_Ms
		result.Clusters = append(result.Clusters, cm)
	}

	if len(clusters) > 0 {
		result.AvgTTFT_Ms = totalTTFT / float64(len(clusters))
	}

	return result, nil
}

func (f *InMemoryMetricsFederator) GetModelMetrics(ctx context.Context, model string) (*ModelMetrics, error) {
	if f.collector != nil {
		return f.modelMetricsFromCollector(ctx, model)
	}
	mm, ok := f.modelMetrics[model]
	if !ok {
		return nil, fmt.Errorf("model %q not found", model)
	}
	return mm, nil
}

func (f *InMemoryMetricsFederator) modelMetricsFromCollector(ctx context.Context, model string) (*ModelMetrics, error) {
	allMetrics, err := f.collector.CollectAll(ctx)
	if err != nil {
		return nil, err
	}

	result := &ModelMetrics{Model: model}
	var ttftSum, cacheSum float64
	var count int

	for _, cm := range allMetrics {
		for _, pool := range cm.Pools {
			poolModel := pool.Model
			if poolModel == "" {
				poolModel = pool.PoolName
			}
			if poolModel == model || pool.PoolName == model {
				result.Clusters = append(result.Clusters, cm.ClusterID)
				result.Throughput += pool.Throughput_TPS
				ttftSum += pool.TTFT_P50_Ms
				result.TTFT_P99_Ms += pool.TTFT_P99_Ms
				cacheSum += pool.KVCacheHitRate
				count++
			}
		}
	}

	if count == 0 {
		return nil, fmt.Errorf("model %q not found", model)
	}
	result.TTFT_P50_Ms = ttftSum / float64(count)
	result.TTFT_P99_Ms = result.TTFT_P99_Ms / float64(count)
	result.CacheHitRate = cacheSum / float64(count)

	return result, nil
}

func (f *InMemoryMetricsFederator) GetTenantMetrics(ctx context.Context, tenantID string) (*TenantMetrics, error) {
	tm, ok := f.tenantMetrics[tenantID]
	if !ok {
		return nil, fmt.Errorf("tenant %q not found", tenantID)
	}
	return tm, nil
}
