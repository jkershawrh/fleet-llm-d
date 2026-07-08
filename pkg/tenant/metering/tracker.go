package metering

import (
	"context"
	"fmt"
	"time"
)

// TimePeriod represents a time range for querying usage data.
type TimePeriod struct {
	Start time.Time
	End   time.Time
}

// TenantUsageSummary holds aggregate usage metrics for a tenant over a time period.
type TenantUsageSummary struct {
	TenantID       string
	TokensConsumed int64
	TotalCost      string
	RequestCount   int64
	AvgLatencyMs   int
}

// ModelUsageSummary holds per-model usage metrics for a tenant over a time period.
type ModelUsageSummary struct {
	Model          string
	TokensConsumed int64
	Cost           string
	RequestCount   int64
	ClustersUsed   []string
}

// UsageTracker provides methods for retrieving tenant and model usage data.
type UsageTracker interface {
	GetUsage(ctx context.Context, tenantID string, period TimePeriod) (*TenantUsageSummary, error)
	GetUsageByModel(ctx context.Context, tenantID string, model string, period TimePeriod) (*ModelUsageSummary, error)
}

// usageKey uniquely identifies a tenant usage record by tenant and time period.
type usageKey struct {
	tenantID string
	start    int64
	end      int64
}

// modelUsageKey uniquely identifies a model usage record by tenant, model, and time period.
type modelUsageKey struct {
	tenantID string
	model    string
	start    int64
	end      int64
}

type defaultUsageTracker struct {
	tenantUsage map[usageKey]*TenantUsageSummary
	modelUsage  map[modelUsageKey]*ModelUsageSummary
}

func makeUsageKey(tenantID string, period TimePeriod) usageKey {
	return usageKey{
		tenantID: tenantID,
		start:    period.Start.Unix(),
		end:      period.End.Unix(),
	}
}

func makeModelUsageKey(tenantID string, model string, period TimePeriod) modelUsageKey {
	return modelUsageKey{
		tenantID: tenantID,
		model:    model,
		start:    period.Start.Unix(),
		end:      period.End.Unix(),
	}
}

// NewUsageTracker returns a new empty UsageTracker instance.
func NewUsageTracker() UsageTracker {
	return &defaultUsageTracker{
		tenantUsage: make(map[usageKey]*TenantUsageSummary),
		modelUsage:  make(map[modelUsageKey]*ModelUsageSummary),
	}
}

// NewSeededUsageTracker returns a UsageTracker pre-seeded with synthetic
// tenant and model usage data. Useful for demos and testing.
func NewSeededUsageTracker() UsageTracker {
	tracker := &defaultUsageTracker{
		tenantUsage: make(map[usageKey]*TenantUsageSummary),
		modelUsage:  make(map[modelUsageKey]*ModelUsageSummary),
	}

	// Pre-seed tenant usage data.
	tracker.tenantUsage[makeUsageKey("tenant-alpha", TimePeriod{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	})] = &TenantUsageSummary{
		TenantID:       "tenant-alpha",
		TokensConsumed: 1_250_000,
		TotalCost:      "18.75",
		RequestCount:   3400,
		AvgLatencyMs:   120,
	}

	tracker.tenantUsage[makeUsageKey("tenant-beta", TimePeriod{
		Start: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
	})] = &TenantUsageSummary{
		TenantID:       "tenant-beta",
		TokensConsumed: 8_400_000,
		TotalCost:      "126.00",
		RequestCount:   22000,
		AvgLatencyMs:   95,
	}

	// Pre-seed model usage data.
	tracker.modelUsage[makeModelUsageKey("tenant-alpha", "meta-llama/Llama-3-70b", TimePeriod{
		Start: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
	})] = &ModelUsageSummary{
		Model:          "meta-llama/Llama-3-70b",
		TokensConsumed: 820_000,
		Cost:           "12.30",
		RequestCount:   2100,
		ClustersUsed:   []string{"us-east-1", "eu-west-1"},
	}

	tracker.modelUsage[makeModelUsageKey("tenant-beta", "mistralai/Mixtral-8x7B", TimePeriod{
		Start: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
	})] = &ModelUsageSummary{
		Model:          "mistralai/Mixtral-8x7B",
		TokensConsumed: 5_600_000,
		Cost:           "42.00",
		RequestCount:   14500,
		ClustersUsed:   []string{"us-west-2"},
	}

	return tracker
}

func (d *defaultUsageTracker) GetUsage(_ context.Context, tenantID string, period TimePeriod) (*TenantUsageSummary, error) {
	key := makeUsageKey(tenantID, period)
	summary, ok := d.tenantUsage[key]
	if !ok {
		return nil, fmt.Errorf("no usage data found for tenant %q in the specified period", tenantID)
	}
	return summary, nil
}

func (d *defaultUsageTracker) GetUsageByModel(_ context.Context, tenantID string, model string, period TimePeriod) (*ModelUsageSummary, error) {
	key := makeModelUsageKey(tenantID, model, period)
	summary, ok := d.modelUsage[key]
	if !ok {
		return nil, fmt.Errorf("no usage data found for tenant %q model %q in the specified period", tenantID, model)
	}
	return summary, nil
}
