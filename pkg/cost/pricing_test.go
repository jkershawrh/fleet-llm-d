package cost

import (
	"math"
	"testing"
)

func TestDefaultPricingTable(t *testing.T) {
	pt := DefaultPricingTable()

	gpuTypes := pt.ListGPUTypes()
	expected := []string{"A100", "B200", "CPU", "H200", "L40", "MI300X"}
	if len(gpuTypes) != len(expected) {
		t.Fatalf("expected %d GPU types, got %d: %v", len(expected), len(gpuTypes), gpuTypes)
	}
	for i, e := range expected {
		if gpuTypes[i] != e {
			t.Fatalf("GPU type[%d] = %q, want %q", i, gpuTypes[i], e)
		}
	}

	tiers := pt.ListTiers()
	expectedTiers := []string{"on-demand", "reserved-1yr", "spot"}
	if len(tiers) != len(expectedTiers) {
		t.Fatalf("expected %d tiers, got %d: %v", len(expectedTiers), len(tiers), tiers)
	}
	for i, e := range expectedTiers {
		if tiers[i] != e {
			t.Fatalf("tier[%d] = %q, want %q", i, tiers[i], e)
		}
	}
}

func TestCostPerHour(t *testing.T) {
	pt := DefaultPricingTable()

	tests := []struct {
		gpuType string
		tier    string
		want    float64
	}{
		{"H200", "on-demand", 4.50},
		{"H200", "reserved-1yr", 2.70},
		{"H200", "spot", 1.35},
		{"A100", "on-demand", 3.20},
		{"CPU", "spot", 0.15},
		{"B200", "on-demand", 5.80},
	}

	for _, tt := range tests {
		cost, err := pt.CostPerHour(tt.gpuType, tt.tier)
		if err != nil {
			t.Fatalf("CostPerHour(%s, %s): %v", tt.gpuType, tt.tier, err)
		}
		if math.Abs(cost-tt.want) > 0.001 {
			t.Fatalf("CostPerHour(%s, %s) = %f, want %f", tt.gpuType, tt.tier, cost, tt.want)
		}
	}
}

func TestCostPerHour_UnknownGPU(t *testing.T) {
	pt := DefaultPricingTable()

	_, err := pt.CostPerHour("NONEXISTENT", "on-demand")
	if err == nil {
		t.Fatal("expected error for unknown GPU type, got nil")
	}

	_, err = pt.CostPerHour("H200", "nonexistent-tier")
	if err == nil {
		t.Fatal("expected error for unknown tier, got nil")
	}
}

func TestCostPerToken(t *testing.T) {
	pt := DefaultPricingTable()

	// H200 on-demand at 1000 tok/s:
	// $4.50/hr / 3600 / 1000 = $0.00000125 per token
	cost, err := pt.CostPerToken("H200", "on-demand", 1000)
	if err != nil {
		t.Fatalf("CostPerToken: %v", err)
	}
	expected := 4.50 / 3600.0 / 1000.0
	if math.Abs(cost-expected) > 1e-12 {
		t.Fatalf("CostPerToken(H200, on-demand, 1000) = %e, want %e", cost, expected)
	}
}

func TestCostPerToken_ZeroThroughput(t *testing.T) {
	pt := DefaultPricingTable()

	_, err := pt.CostPerToken("H200", "on-demand", 0)
	if err == nil {
		t.Fatal("expected error for zero throughput, got nil")
	}

	_, err = pt.CostPerToken("H200", "on-demand", -100)
	if err == nil {
		t.Fatal("expected error for negative throughput, got nil")
	}
}

func TestListGPUTypes(t *testing.T) {
	pt := DefaultPricingTable()

	types := pt.ListGPUTypes()
	if len(types) != 6 {
		t.Fatalf("expected 6 GPU types, got %d: %v", len(types), types)
	}

	// Verify sorted order.
	for i := 1; i < len(types); i++ {
		if types[i] < types[i-1] {
			t.Fatalf("GPU types not sorted: %v", types)
		}
	}

	// Add a custom GPU and verify it appears.
	pt.SetPricing(GPUPricing{GPUType: "H100", CostPerHour: 3.80, MemoryGB: 80, PricingTier: "on-demand"})
	types = pt.ListGPUTypes()
	if len(types) != 7 {
		t.Fatalf("expected 7 GPU types after adding H100, got %d: %v", len(types), types)
	}

	foundH100 := false
	for _, tp := range types {
		if tp == "H100" {
			foundH100 = true
			break
		}
	}
	if !foundH100 {
		t.Fatalf("H100 not found in GPU types after SetPricing: %v", types)
	}
}
