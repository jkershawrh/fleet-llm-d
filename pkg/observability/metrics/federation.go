package metrics

import (
	"context"
	"fmt"
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
}

// NewMetricsFederator returns a new MetricsFederator instance pre-populated with cluster data.
func NewMetricsFederator() MetricsFederator {
	f := &InMemoryMetricsFederator{
		clusterMetrics: map[string]ClusterMetricsSummary{
			"us-east-1": {
				ClusterID:  "us-east-1",
				GPUs:       8,
				Models:     2,
				Throughput: 500.0,
				AvgTTFT_Ms: 50.0,
			},
			"us-west-2": {
				ClusterID:  "us-west-2",
				GPUs:       8,
				Models:     2,
				Throughput: 500.0,
				AvgTTFT_Ms: 55.0,
			},
			"eu-central-1": {
				ClusterID:  "eu-central-1",
				GPUs:       8,
				Models:     1,
				Throughput: 500.0,
				AvgTTFT_Ms: 60.0,
			},
		},
		modelMetrics: map[string]*ModelMetrics{
			"granite-3b": {
				Model:        "granite-3b",
				Clusters:     []string{"us-east-1", "us-west-2", "eu-central-1"},
				Throughput:   800.0,
				TTFT_P50_Ms:  45.0,
				TTFT_P95_Ms:  120.0,
				TTFT_P99_Ms:  250.0,
				CacheHitRate: 0.85,
			},
			"llama-70b": {
				Model:        "llama-70b",
				Clusters:     []string{"us-east-1", "us-west-2"},
				Throughput:   200.0,
				TTFT_P50_Ms:  150.0,
				TTFT_P95_Ms:  400.0,
				TTFT_P99_Ms:  800.0,
				CacheHitRate: 0.70,
			},
		},
		tenantMetrics: map[string]*TenantMetrics{
			"tenant-prod-001": {
				TenantID:       "tenant-prod-001",
				TokensConsumed: 5000000,
				Cost:           "125.50",
				AvgLatencyMs:   85,
			},
			"tenant-staging-001": {
				TenantID:       "tenant-staging-001",
				TokensConsumed: 500000,
				Cost:           "12.75",
				AvgLatencyMs:   95,
			},
		},
	}
	return f
}

func (f *InMemoryMetricsFederator) FederateMetrics(ctx context.Context, clusters []string) (*FleetMetrics, error) {
	result := &FleetMetrics{}

	var totalTTFT float64

	for _, clusterID := range clusters {
		cm, ok := f.clusterMetrics[clusterID]
		if !ok {
			return nil, fmt.Errorf("cluster %q not found", clusterID)
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
	mm, ok := f.modelMetrics[model]
	if !ok {
		return nil, fmt.Errorf("model %q not found", model)
	}
	return mm, nil
}

func (f *InMemoryMetricsFederator) GetTenantMetrics(ctx context.Context, tenantID string) (*TenantMetrics, error) {
	tm, ok := f.tenantMetrics[tenantID]
	if !ok {
		return nil, fmt.Errorf("tenant %q not found", tenantID)
	}
	return tm, nil
}
