package cost

import (
	"math"
	"testing"
)

func TestComputeTokenCost_H200(t *testing.T) {
	pt := DefaultPricingTable()

	tc, err := ComputeTokenCost("granite-3b", "H200", "on-demand", 1000, pt)
	if err != nil {
		t.Fatalf("ComputeTokenCost: %v", err)
	}

	if tc.Model != "granite-3b" {
		t.Fatalf("model = %q, want 'granite-3b'", tc.Model)
	}
	if tc.GPUType != "H200" {
		t.Fatalf("gpu_type = %q, want 'H200'", tc.GPUType)
	}
	if tc.CostPerHour != 4.50 {
		t.Fatalf("cost_per_hour = %f, want 4.50", tc.CostPerHour)
	}

	// Expected: $4.50/hr / 3600s / 1000 tok/s * 1M = $1.25 per M tokens
	expectedCPMT := 4.50 / 3600.0 / 1000.0 * 1_000_000.0
	if math.Abs(tc.CostPerMToken-expectedCPMT) > 0.01 {
		t.Fatalf("CostPerMToken = %f, want ~%f", tc.CostPerMToken, expectedCPMT)
	}
}

func TestComputeTokenCost_CPU(t *testing.T) {
	pt := DefaultPricingTable()

	tc, err := ComputeTokenCost("granite-3b", "CPU", "on-demand", 50, pt)
	if err != nil {
		t.Fatalf("ComputeTokenCost: %v", err)
	}

	if tc.GPUType != "CPU" {
		t.Fatalf("gpu_type = %q, want 'CPU'", tc.GPUType)
	}
	if tc.CostPerHour != 0.50 {
		t.Fatalf("cost_per_hour = %f, want 0.50", tc.CostPerHour)
	}

	// Expected: $0.50/hr / 3600s / 50 tok/s * 1M = ~$2.78 per M tokens
	expectedCPMT := 0.50 / 3600.0 / 50.0 * 1_000_000.0
	if math.Abs(tc.CostPerMToken-expectedCPMT) > 0.01 {
		t.Fatalf("CostPerMToken = %f, want ~%f", tc.CostPerMToken, expectedCPMT)
	}
}

func TestProjectMonthlyCost(t *testing.T) {
	pt := DefaultPricingTable()

	tc, err := ComputeTokenCost("granite-3b", "H200", "on-demand", 1000, pt)
	if err != nil {
		t.Fatalf("ComputeTokenCost: %v", err)
	}

	// 10M tokens/day * 30 days = 300M tokens/month
	// Cost per M tokens ~$1.25 -> ~$375/month
	monthly := ProjectMonthlyCost(10_000_000, tc)
	expectedMonthly := float64(10_000_000) * 30.0 * tc.CostPerMToken / 1_000_000.0
	if math.Abs(monthly-expectedMonthly) > 0.01 {
		t.Fatalf("ProjectMonthlyCost = %f, want %f", monthly, expectedMonthly)
	}
	if monthly <= 0 {
		t.Fatal("monthly cost should be positive")
	}
}

func TestProjectSavings(t *testing.T) {
	savings := ProjectSavings(1000.0, 600.0)

	if savings.CurrentMonthly != 1000.0 {
		t.Fatalf("CurrentMonthly = %f, want 1000", savings.CurrentMonthly)
	}
	if savings.OptimizedMonthly != 600.0 {
		t.Fatalf("OptimizedMonthly = %f, want 600", savings.OptimizedMonthly)
	}
	if math.Abs(savings.MonthlySavings-400.0) > 0.01 {
		t.Fatalf("MonthlySavings = %f, want 400", savings.MonthlySavings)
	}
	if math.Abs(savings.SavingsPercent-40.0) > 0.01 {
		t.Fatalf("SavingsPercent = %f, want 40", savings.SavingsPercent)
	}
	if math.Abs(savings.Annualized-4800.0) > 0.01 {
		t.Fatalf("Annualized = %f, want 4800", savings.Annualized)
	}
}

func TestCompareGPUCosts_SortedByCost(t *testing.T) {
	pt := DefaultPricingTable()

	throughputs := map[string]float64{
		"H200":  2000,
		"A100":  1000,
		"CPU":   50,
		"L40":   500,
		"B200":  2500,
		"MI300X": 1500,
	}

	results := CompareGPUCosts("granite-3b", throughputs, pt)
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}

	// Verify sorted by CostPerMToken ascending.
	for i := 1; i < len(results); i++ {
		if results[i].CostPerMToken < results[i-1].CostPerMToken {
			t.Fatalf("results not sorted by CostPerMToken: [%d]=%f > [%d]=%f",
				i-1, results[i-1].CostPerMToken, i, results[i].CostPerMToken)
		}
	}

	// The cheapest should not be CPU (it has low throughput).
	if results[0].GPUType == "CPU" {
		t.Fatal("CPU should not be cheapest per-token given its low throughput")
	}
}
