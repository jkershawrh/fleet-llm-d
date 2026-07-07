package cost

import (
	"math"
	"testing"
	"time"
)

func TestChargebackReport_SingleModel(t *testing.T) {
	pt := DefaultPricingTable()
	now := time.Now()

	usage := []UsageRecord{
		{
			TenantID:  "tenant-1",
			Model:     "granite-3b",
			Cluster:   "us-east",
			GPUType:   "H200",
			Tokens:    1_000_000,
			Duration:  2 * time.Hour,
			Timestamp: now,
		},
	}

	report := GenerateChargebackReport("tenant-1", usage, pt, 100.0)

	if report.TenantID != "tenant-1" {
		t.Fatalf("TenantID = %q, want 'tenant-1'", report.TenantID)
	}
	if report.TokensConsumed != 1_000_000 {
		t.Fatalf("TokensConsumed = %d, want 1000000", report.TokensConsumed)
	}
	if math.Abs(report.GPUHoursUsed-2.0) > 0.001 {
		t.Fatalf("GPUHoursUsed = %f, want 2.0", report.GPUHoursUsed)
	}
	// H200 on-demand = $4.50/hr, 2 hours = $9.00
	expectedCost := 4.50 * 2.0
	if math.Abs(report.TotalCost-expectedCost) > 0.01 {
		t.Fatalf("TotalCost = %f, want %f", report.TotalCost, expectedCost)
	}
	if len(report.CostBreakdown) != 1 {
		t.Fatalf("expected 1 line item, got %d", len(report.CostBreakdown))
	}
}

func TestChargebackReport_MultiModel(t *testing.T) {
	pt := DefaultPricingTable()
	now := time.Now()

	usage := []UsageRecord{
		{
			TenantID:  "tenant-1",
			Model:     "granite-3b",
			Cluster:   "us-east",
			GPUType:   "H200",
			Tokens:    500_000,
			Duration:  1 * time.Hour,
			Timestamp: now,
		},
		{
			TenantID:  "tenant-1",
			Model:     "llama-70b",
			Cluster:   "eu-west",
			GPUType:   "A100",
			Tokens:    300_000,
			Duration:  3 * time.Hour,
			Timestamp: now.Add(-time.Hour),
		},
		{
			TenantID:  "tenant-1",
			Model:     "granite-3b",
			Cluster:   "us-east",
			GPUType:   "H200",
			Tokens:    200_000,
			Duration:  30 * time.Minute,
			Timestamp: now.Add(time.Hour),
		},
	}

	report := GenerateChargebackReport("tenant-1", usage, pt, 1000.0)

	if report.TokensConsumed != 1_000_000 {
		t.Fatalf("TokensConsumed = %d, want 1000000", report.TokensConsumed)
	}

	// 2 line items: granite-3b/us-east and llama-70b/eu-west
	if len(report.CostBreakdown) != 2 {
		t.Fatalf("expected 2 line items, got %d", len(report.CostBreakdown))
	}

	// Verify total cost is the sum of line items.
	var sumCost float64
	for _, item := range report.CostBreakdown {
		sumCost += item.Cost
	}
	if math.Abs(report.TotalCost-sumCost) > 0.01 {
		t.Fatalf("TotalCost (%f) != sum of line items (%f)", report.TotalCost, sumCost)
	}

	// granite-3b: 1hr + 0.5hr = 1.5hr on H200 ($4.50/hr) = $6.75
	// llama-70b: 3hr on A100 ($3.20/hr) = $9.60
	expectedTotal := 4.50*1.5 + 3.20*3.0
	if math.Abs(report.TotalCost-expectedTotal) > 0.01 {
		t.Fatalf("TotalCost = %f, want %f", report.TotalCost, expectedTotal)
	}
}

func TestChargebackReport_BudgetUsage(t *testing.T) {
	pt := DefaultPricingTable()
	now := time.Now()

	usage := []UsageRecord{
		{
			TenantID:  "tenant-1",
			Model:     "granite-3b",
			Cluster:   "us-east",
			GPUType:   "A100",
			Tokens:    1_000_000,
			Duration:  10 * time.Hour,
			Timestamp: now,
		},
	}

	// A100 on-demand = $3.20/hr, 10 hours = $32.00
	// Budget = $100 -> 32% used
	report := GenerateChargebackReport("tenant-1", usage, pt, 100.0)

	expectedBudgetUsed := (3.20 * 10.0 / 100.0) * 100.0
	if math.Abs(report.BudgetUsed-expectedBudgetUsed) > 0.01 {
		t.Fatalf("BudgetUsed = %f%%, want %f%%", report.BudgetUsed, expectedBudgetUsed)
	}
	if report.BudgetTotal != 100.0 {
		t.Fatalf("BudgetTotal = %f, want 100.0", report.BudgetTotal)
	}
}

func TestChargebackReport_NoUsage(t *testing.T) {
	pt := DefaultPricingTable()

	report := GenerateChargebackReport("tenant-1", nil, pt, 500.0)

	if report.TenantID != "tenant-1" {
		t.Fatalf("TenantID = %q, want 'tenant-1'", report.TenantID)
	}
	if report.TokensConsumed != 0 {
		t.Fatalf("TokensConsumed = %d, want 0", report.TokensConsumed)
	}
	if report.TotalCost != 0 {
		t.Fatalf("TotalCost = %f, want 0", report.TotalCost)
	}
	if report.GPUHoursUsed != 0 {
		t.Fatalf("GPUHoursUsed = %f, want 0", report.GPUHoursUsed)
	}
	if len(report.CostBreakdown) != 0 {
		t.Fatalf("CostBreakdown should be empty, got %d items", len(report.CostBreakdown))
	}
	if report.BudgetTotal != 500.0 {
		t.Fatalf("BudgetTotal = %f, want 500.0", report.BudgetTotal)
	}
}
