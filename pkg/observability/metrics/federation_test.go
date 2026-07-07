package metrics

import (
	"context"
	"testing"
)

func TestFederateMetrics(t *testing.T) {
	tests := []struct {
		name            string
		clusters        []string
		wantTotalGPUs   int
		wantModels      int
		wantThroughput  float64
		wantClusterCount int
	}{
		{
			name:            "federate metrics from multiple clusters",
			clusters:        []string{"us-east-1", "us-west-2", "eu-central-1"},
			wantTotalGPUs:   24,
			wantModels:      5,
			wantThroughput:  1500.0,
			wantClusterCount: 3,
		},
		{
			name:            "federate metrics from single cluster",
			clusters:        []string{"us-east-1"},
			wantTotalGPUs:   8,
			wantModels:      2,
			wantThroughput:  500.0,
			wantClusterCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fed := NewMetricsFederator()
			result, err := fed.FederateMetrics(context.Background(), tt.clusters)
			if err != nil {
				t.Fatalf("FederateMetrics() returned error: %v", err)
			}
			if result == nil {
				t.Fatal("FederateMetrics() returned nil result")
			}
			if result.TotalGPUs != tt.wantTotalGPUs {
				t.Errorf("TotalGPUs = %d, want %d", result.TotalGPUs, tt.wantTotalGPUs)
			}
			if result.ActiveModels != tt.wantModels {
				t.Errorf("ActiveModels = %d, want %d", result.ActiveModels, tt.wantModels)
			}
			if result.TotalThroughput != tt.wantThroughput {
				t.Errorf("TotalThroughput = %f, want %f", result.TotalThroughput, tt.wantThroughput)
			}
			if len(result.Clusters) != tt.wantClusterCount {
				t.Errorf("Clusters count = %d, want %d", len(result.Clusters), tt.wantClusterCount)
			}
		})
	}
}

func TestGetModelMetrics(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		wantThroughput float64
		wantP50        float64
		wantP95        float64
		wantP99        float64
		wantClusters   int
	}{
		{
			name:           "get metrics for granite model",
			model:          "granite-3b",
			wantThroughput: 800.0,
			wantP50:        45.0,
			wantP95:        120.0,
			wantP99:        250.0,
			wantClusters:   3,
		},
		{
			name:           "get metrics for llama model",
			model:          "llama-70b",
			wantThroughput: 200.0,
			wantP50:        150.0,
			wantP95:        400.0,
			wantP99:        800.0,
			wantClusters:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fed := NewMetricsFederator()
			result, err := fed.GetModelMetrics(context.Background(), tt.model)
			if err != nil {
				t.Fatalf("GetModelMetrics() returned error: %v", err)
			}
			if result == nil {
				t.Fatal("GetModelMetrics() returned nil result")
			}
			if result.Model != tt.model {
				t.Errorf("Model = %s, want %s", result.Model, tt.model)
			}
			if result.Throughput != tt.wantThroughput {
				t.Errorf("Throughput = %f, want %f", result.Throughput, tt.wantThroughput)
			}
			if result.TTFT_P50_Ms != tt.wantP50 {
				t.Errorf("TTFT_P50_Ms = %f, want %f", result.TTFT_P50_Ms, tt.wantP50)
			}
			if result.TTFT_P95_Ms != tt.wantP95 {
				t.Errorf("TTFT_P95_Ms = %f, want %f", result.TTFT_P95_Ms, tt.wantP95)
			}
			if result.TTFT_P99_Ms != tt.wantP99 {
				t.Errorf("TTFT_P99_Ms = %f, want %f", result.TTFT_P99_Ms, tt.wantP99)
			}
			if len(result.Clusters) != tt.wantClusters {
				t.Errorf("Clusters count = %d, want %d", len(result.Clusters), tt.wantClusters)
			}
		})
	}
}

func TestGetTenantMetrics(t *testing.T) {
	tests := []struct {
		name             string
		tenantID         string
		wantTokens       int64
		wantCost         string
		wantAvgLatencyMs int
	}{
		{
			name:             "get metrics for production tenant",
			tenantID:         "tenant-prod-001",
			wantTokens:       5000000,
			wantCost:         "125.50",
			wantAvgLatencyMs: 85,
		},
		{
			name:             "get metrics for staging tenant",
			tenantID:         "tenant-staging-001",
			wantTokens:       500000,
			wantCost:         "12.75",
			wantAvgLatencyMs: 95,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fed := NewMetricsFederator()
			result, err := fed.GetTenantMetrics(context.Background(), tt.tenantID)
			if err != nil {
				t.Fatalf("GetTenantMetrics() returned error: %v", err)
			}
			if result == nil {
				t.Fatal("GetTenantMetrics() returned nil result")
			}
			if result.TenantID != tt.tenantID {
				t.Errorf("TenantID = %s, want %s", result.TenantID, tt.tenantID)
			}
			if result.TokensConsumed != tt.wantTokens {
				t.Errorf("TokensConsumed = %d, want %d", result.TokensConsumed, tt.wantTokens)
			}
			if result.Cost != tt.wantCost {
				t.Errorf("Cost = %s, want %s", result.Cost, tt.wantCost)
			}
			if result.AvgLatencyMs != tt.wantAvgLatencyMs {
				t.Errorf("AvgLatencyMs = %d, want %d", result.AvgLatencyMs, tt.wantAvgLatencyMs)
			}
		})
	}
}
