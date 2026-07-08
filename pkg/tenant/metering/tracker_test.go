package metering

import (
	"context"
	"testing"
	"time"
)

func TestNewUsageTracker_StartsEmpty(t *testing.T) {
	tracker := NewUsageTracker()
	period := TimePeriod{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	}
	_, err := tracker.GetUsage(context.Background(), "tenant-alpha", period)
	if err == nil {
		t.Error("empty tracker should return error for unknown tenant")
	}
}

func TestGetUsage(t *testing.T) {
	tracker := NewSeededUsageTracker()

	tests := []struct {
		name     string
		tenantID string
		period   TimePeriod
		want     *TenantUsageSummary
	}{
		{
			name:     "daily usage for tenant alpha",
			tenantID: "tenant-alpha",
			period: TimePeriod{
				Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			},
			want: &TenantUsageSummary{
				TenantID:       "tenant-alpha",
				TokensConsumed: 1_250_000,
				TotalCost:      "18.75",
				RequestCount:   3400,
				AvgLatencyMs:   120,
			},
		},
		{
			name:     "weekly usage for tenant beta",
			tenantID: "tenant-beta",
			period: TimePeriod{
				Start: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
			},
			want: &TenantUsageSummary{
				TenantID:       "tenant-beta",
				TokensConsumed: 8_400_000,
				TotalCost:      "126.00",
				RequestCount:   22000,
				AvgLatencyMs:   95,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tracker.GetUsage(context.Background(), tt.tenantID, tt.period)
			if err != nil {
				t.Fatalf("GetUsage() returned unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("GetUsage() returned nil summary")
			}
			if got.TenantID != tt.want.TenantID {
				t.Errorf("TenantID = %q, want %q", got.TenantID, tt.want.TenantID)
			}
			if got.TokensConsumed != tt.want.TokensConsumed {
				t.Errorf("TokensConsumed = %d, want %d", got.TokensConsumed, tt.want.TokensConsumed)
			}
			if got.TotalCost != tt.want.TotalCost {
				t.Errorf("TotalCost = %q, want %q", got.TotalCost, tt.want.TotalCost)
			}
			if got.RequestCount != tt.want.RequestCount {
				t.Errorf("RequestCount = %d, want %d", got.RequestCount, tt.want.RequestCount)
			}
			if got.AvgLatencyMs != tt.want.AvgLatencyMs {
				t.Errorf("AvgLatencyMs = %d, want %d", got.AvgLatencyMs, tt.want.AvgLatencyMs)
			}
		})
	}
}

func TestGetUsageByModel(t *testing.T) {
	tracker := NewSeededUsageTracker()

	tests := []struct {
		name     string
		tenantID string
		model    string
		period   TimePeriod
		want     *ModelUsageSummary
	}{
		{
			name:     "llama-3 usage for tenant alpha over one day",
			tenantID: "tenant-alpha",
			model:    "meta-llama/Llama-3-70b",
			period: TimePeriod{
				Start: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
			},
			want: &ModelUsageSummary{
				Model:          "meta-llama/Llama-3-70b",
				TokensConsumed: 820_000,
				Cost:           "12.30",
				RequestCount:   2100,
				ClustersUsed:   []string{"us-east-1", "eu-west-1"},
			},
		},
		{
			name:     "mistral usage for tenant beta over one week",
			tenantID: "tenant-beta",
			model:    "mistralai/Mixtral-8x7B",
			period: TimePeriod{
				Start: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
			},
			want: &ModelUsageSummary{
				Model:          "mistralai/Mixtral-8x7B",
				TokensConsumed: 5_600_000,
				Cost:           "42.00",
				RequestCount:   14500,
				ClustersUsed:   []string{"us-west-2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tracker.GetUsageByModel(context.Background(), tt.tenantID, tt.model, tt.period)
			if err != nil {
				t.Fatalf("GetUsageByModel() returned unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("GetUsageByModel() returned nil summary")
			}
			if got.Model != tt.want.Model {
				t.Errorf("Model = %q, want %q", got.Model, tt.want.Model)
			}
			if got.TokensConsumed != tt.want.TokensConsumed {
				t.Errorf("TokensConsumed = %d, want %d", got.TokensConsumed, tt.want.TokensConsumed)
			}
			if got.Cost != tt.want.Cost {
				t.Errorf("Cost = %q, want %q", got.Cost, tt.want.Cost)
			}
			if got.RequestCount != tt.want.RequestCount {
				t.Errorf("RequestCount = %d, want %d", got.RequestCount, tt.want.RequestCount)
			}
			if len(got.ClustersUsed) != len(tt.want.ClustersUsed) {
				t.Errorf("ClustersUsed length = %d, want %d", len(got.ClustersUsed), len(tt.want.ClustersUsed))
			} else {
				for i, cluster := range got.ClustersUsed {
					if cluster != tt.want.ClustersUsed[i] {
						t.Errorf("ClustersUsed[%d] = %q, want %q", i, cluster, tt.want.ClustersUsed[i])
					}
				}
			}
		})
	}
}
