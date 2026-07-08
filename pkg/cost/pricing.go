package cost

import (
	"fmt"
	"sort"
)

// GPUPricing describes the hourly cost and memory capacity for a GPU type
// at a specific pricing tier.
type GPUPricing struct {
	GPUType     string  `json:"gpu_type"`
	CostPerHour float64 `json:"cost_per_hour"`
	MemoryGB    int     `json:"memory_gb"`
	PricingTier string  `json:"pricing_tier"`
}

// PricingTable holds per-GPU-type, per-tier pricing information.
type PricingTable struct {
	prices map[string]map[string]GPUPricing // gpuType -> tier -> pricing
}

// DefaultPricingTable returns a PricingTable pre-populated with real-world
// cloud GPU rates.
func DefaultPricingTable() *PricingTable {
	t := &PricingTable{
		prices: make(map[string]map[string]GPUPricing),
	}

	defaults := []GPUPricing{
		// H200
		{GPUType: "H200", CostPerHour: 4.50, MemoryGB: 141, PricingTier: "on-demand"},
		{GPUType: "H200", CostPerHour: 2.70, MemoryGB: 141, PricingTier: "reserved-1yr"},
		{GPUType: "H200", CostPerHour: 1.35, MemoryGB: 141, PricingTier: "spot"},
		// B200
		{GPUType: "B200", CostPerHour: 5.80, MemoryGB: 192, PricingTier: "on-demand"},
		{GPUType: "B200", CostPerHour: 3.48, MemoryGB: 192, PricingTier: "reserved-1yr"},
		{GPUType: "B200", CostPerHour: 1.74, MemoryGB: 192, PricingTier: "spot"},
		// H100
		{GPUType: "H100", CostPerHour: 3.50, MemoryGB: 80, PricingTier: "on-demand"},
		{GPUType: "H100", CostPerHour: 2.10, MemoryGB: 80, PricingTier: "reserved-1yr"},
		{GPUType: "H100", CostPerHour: 1.05, MemoryGB: 80, PricingTier: "spot"},
		// A100
		{GPUType: "A100", CostPerHour: 3.20, MemoryGB: 80, PricingTier: "on-demand"},
		{GPUType: "A100", CostPerHour: 1.92, MemoryGB: 80, PricingTier: "reserved-1yr"},
		{GPUType: "A100", CostPerHour: 0.96, MemoryGB: 80, PricingTier: "spot"},
		// MI300X
		{GPUType: "MI300X", CostPerHour: 4.00, MemoryGB: 192, PricingTier: "on-demand"},
		{GPUType: "MI300X", CostPerHour: 2.40, MemoryGB: 192, PricingTier: "reserved-1yr"},
		{GPUType: "MI300X", CostPerHour: 1.20, MemoryGB: 192, PricingTier: "spot"},
		// L40
		{GPUType: "L40", CostPerHour: 1.80, MemoryGB: 48, PricingTier: "on-demand"},
		{GPUType: "L40", CostPerHour: 1.08, MemoryGB: 48, PricingTier: "reserved-1yr"},
		{GPUType: "L40", CostPerHour: 0.54, MemoryGB: 48, PricingTier: "spot"},
		// CPU
		{GPUType: "CPU", CostPerHour: 0.50, MemoryGB: 0, PricingTier: "on-demand"},
		{GPUType: "CPU", CostPerHour: 0.30, MemoryGB: 0, PricingTier: "reserved-1yr"},
		{GPUType: "CPU", CostPerHour: 0.15, MemoryGB: 0, PricingTier: "spot"},
	}

	for _, p := range defaults {
		t.SetPricing(p)
	}

	return t
}

// CostPerHour returns the hourly cost for a GPU type at the given pricing
// tier. It returns an error if the GPU type or tier is unknown.
func (t *PricingTable) CostPerHour(gpuType, tier string) (float64, error) {
	tiers, ok := t.prices[gpuType]
	if !ok {
		return 0, fmt.Errorf("unknown GPU type: %s", gpuType)
	}
	pricing, ok := tiers[tier]
	if !ok {
		return 0, fmt.Errorf("unknown pricing tier %q for GPU type %s", tier, gpuType)
	}
	return pricing.CostPerHour, nil
}

// CostPerToken returns the cost of producing a single token on the given GPU
// type and pricing tier, assuming the specified throughput in tokens per
// second. It returns an error if throughput is zero or the GPU/tier is unknown.
func (t *PricingTable) CostPerToken(gpuType, tier string, tokensPerSecond float64) (float64, error) {
	if tokensPerSecond <= 0 {
		return 0, fmt.Errorf("tokens per second must be positive, got %f", tokensPerSecond)
	}
	hourly, err := t.CostPerHour(gpuType, tier)
	if err != nil {
		return 0, err
	}
	return hourly / 3600.0 / tokensPerSecond, nil
}

// ListGPUTypes returns the sorted list of GPU types known to this table.
func (t *PricingTable) ListGPUTypes() []string {
	types := make([]string, 0, len(t.prices))
	for k := range t.prices {
		types = append(types, k)
	}
	sort.Strings(types)
	return types
}

// ListTiers returns the sorted, deduplicated list of pricing tiers across all
// GPU types.
func (t *PricingTable) ListTiers() []string {
	seen := make(map[string]bool)
	for _, tiers := range t.prices {
		for tier := range tiers {
			seen[tier] = true
		}
	}
	result := make([]string, 0, len(seen))
	for tier := range seen {
		result = append(result, tier)
	}
	sort.Strings(result)
	return result
}

// SetPricing adds or updates a pricing entry.
func (t *PricingTable) SetPricing(pricing GPUPricing) {
	if t.prices[pricing.GPUType] == nil {
		t.prices[pricing.GPUType] = make(map[string]GPUPricing)
	}
	t.prices[pricing.GPUType][pricing.PricingTier] = pricing
}

// AllPrices returns a flat slice of every GPUPricing entry in the table,
// useful for JSON serialization.
func (t *PricingTable) AllPrices() []GPUPricing {
	var out []GPUPricing
	for _, tiers := range t.prices {
		for _, p := range tiers {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GPUType != out[j].GPUType {
			return out[i].GPUType < out[j].GPUType
		}
		return out[i].PricingTier < out[j].PricingTier
	})
	return out
}
