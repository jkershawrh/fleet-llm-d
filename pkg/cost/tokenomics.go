package cost

import (
	"fmt"
	"sort"
)

// TokenCost holds the computed cost metrics for serving a model on a specific
// GPU type and pricing tier.
type TokenCost struct {
	Model         string  `json:"model"`
	GPUType       string  `json:"gpu_type"`
	PricingTier   string  `json:"pricing_tier"`
	CostPerMToken float64 `json:"cost_per_m_token"` // cost per million tokens
	ThroughputTPS float64 `json:"throughput_tps"`
	CostPerHour   float64 `json:"cost_per_hour"`
}

// SavingsProjection projects the financial impact of moving from a current
// cost structure to an optimized one.
type SavingsProjection struct {
	CurrentMonthly   float64 `json:"current_monthly"`
	OptimizedMonthly float64 `json:"optimized_monthly"`
	MonthlySavings   float64 `json:"monthly_savings"`
	SavingsPercent   float64 `json:"savings_percent"`
	Annualized       float64 `json:"annualized"`
}

// ComputeTokenCost calculates the cost metrics for running a model on a
// specific GPU type and pricing tier at the given throughput.
func ComputeTokenCost(model, gpuType, tier string, throughputTPS float64, pricing *PricingTable) (*TokenCost, error) {
	if throughputTPS <= 0 {
		return nil, fmt.Errorf("throughput must be positive, got %f", throughputTPS)
	}

	costPerHour, err := pricing.CostPerHour(gpuType, tier)
	if err != nil {
		return nil, err
	}

	costPerToken, err := pricing.CostPerToken(gpuType, tier, throughputTPS)
	if err != nil {
		return nil, err
	}

	costPerMToken := costPerToken * 1_000_000

	return &TokenCost{
		Model:         model,
		GPUType:       gpuType,
		PricingTier:   tier,
		CostPerMToken: costPerMToken,
		ThroughputTPS: throughputTPS,
		CostPerHour:   costPerHour,
	}, nil
}

// ProjectMonthlyCost projects the total monthly cost given a daily token volume
// and a per-token cost structure.
func ProjectMonthlyCost(tokensPerDay int64, tokenCost *TokenCost) float64 {
	// 30 days per month
	totalTokens := float64(tokensPerDay) * 30.0
	return totalTokens * tokenCost.CostPerMToken / 1_000_000.0
}

// ProjectSavings computes the savings when moving from currentMonthly cost to
// optimizedMonthly cost.
func ProjectSavings(currentMonthly, optimizedMonthly float64) SavingsProjection {
	savings := currentMonthly - optimizedMonthly
	var percent float64
	if currentMonthly > 0 {
		percent = (savings / currentMonthly) * 100.0
	}
	return SavingsProjection{
		CurrentMonthly:   currentMonthly,
		OptimizedMonthly: optimizedMonthly,
		MonthlySavings:   savings,
		SavingsPercent:   percent,
		Annualized:       savings * 12.0,
	}
}

// CompareGPUCosts computes token costs for a model across multiple GPU types
// using on-demand pricing and the provided throughputs. Results are sorted by
// CostPerMToken ascending (cheapest first).
func CompareGPUCosts(model string, throughputs map[string]float64, pricing *PricingTable) []TokenCost {
	var results []TokenCost
	for gpuType, tps := range throughputs {
		tc, err := ComputeTokenCost(model, gpuType, "on-demand", tps, pricing)
		if err != nil {
			continue
		}
		results = append(results, *tc)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].CostPerMToken < results[j].CostPerMToken
	})
	return results
}
