package cost

import (
	"log/slog"
	"sort"
	"time"
)

// TimePeriod represents a time range for a chargeback report.
type TimePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// ChargebackReport is the chargeback summary for a single tenant over a time
// period.
type ChargebackReport struct {
	TenantID       string         `json:"tenant_id"`
	Period         TimePeriod     `json:"period"`
	TokensConsumed int64          `json:"tokens_consumed"`
	GPUHoursUsed   float64        `json:"gpu_hours_used"`
	TotalCost      float64        `json:"total_cost"`
	CostBreakdown  []CostLineItem `json:"cost_breakdown"`
	BudgetTotal    float64        `json:"budget_total"`
	BudgetUsed     float64        `json:"budget_used"` // percentage 0-100
}

// CostLineItem is a single line in a chargeback report, grouped by
// model+cluster combination.
type CostLineItem struct {
	Model    string  `json:"model"`
	Cluster  string  `json:"cluster"`
	GPUType  string  `json:"gpu_type"`
	Tokens   int64   `json:"tokens"`
	GPUHours float64 `json:"gpu_hours"`
	Cost     float64 `json:"cost"`
	Unpriced bool    `json:"unpriced,omitempty"`
}

// UsageRecord is a single usage event recorded by the metering system.
type UsageRecord struct {
	TenantID  string        `json:"tenant_id"`
	Model     string        `json:"model"`
	Cluster   string        `json:"cluster"`
	GPUType   string        `json:"gpu_type"`
	Tokens    int64         `json:"tokens"`
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
}

// lineItemKey groups usage by model+cluster.
type lineItemKey struct {
	Model   string
	Cluster string
	GPUType string
}

// GenerateChargebackReport produces a chargeback report for the given tenant
// using the provided usage records and pricing table. Budget is the total
// budget cap; BudgetUsed is the percentage consumed.
func GenerateChargebackReport(tenantID string, usage []UsageRecord, pricing *PricingTable, budget float64) *ChargebackReport {
	report := &ChargebackReport{
		TenantID:    tenantID,
		BudgetTotal: budget,
	}

	if len(usage) == 0 {
		return report
	}

	// Determine the time period from the usage records.
	minTime := usage[0].Timestamp
	maxTime := usage[0].Timestamp
	for _, u := range usage[1:] {
		if u.Timestamp.Before(minTime) {
			minTime = u.Timestamp
		}
		if u.Timestamp.After(maxTime) {
			maxTime = u.Timestamp
		}
	}
	report.Period = TimePeriod{Start: minTime, End: maxTime}

	// Group by model+cluster+gpuType.
	groups := make(map[lineItemKey]*CostLineItem)
	for _, u := range usage {
		key := lineItemKey{Model: u.Model, Cluster: u.Cluster, GPUType: u.GPUType}
		item, ok := groups[key]
		if !ok {
			item = &CostLineItem{
				Model:   u.Model,
				Cluster: u.Cluster,
				GPUType: u.GPUType,
			}
			groups[key] = item
		}
		item.Tokens += u.Tokens
		gpuHours := u.Duration.Hours()
		item.GPUHours += gpuHours

		// Compute cost from GPU hours and pricing table.
		costPerHour, err := pricing.CostPerHour(u.GPUType, "on-demand")
		if err != nil {
			// Fall back to zero cost if GPU type is unknown.
			costPerHour = 0
			item.Unpriced = true
			slog.Info("WARNING: unknown GPU type %q for tenant %s — cost will be $0", u.GPUType, tenantID)
		}
		item.Cost += gpuHours * costPerHour
	}

	// Flatten to slice and accumulate totals.
	var totalCost float64
	var totalTokens int64
	var totalGPUHours float64

	breakdown := make([]CostLineItem, 0, len(groups))
	for _, item := range groups {
		breakdown = append(breakdown, *item)
		totalCost += item.Cost
		totalTokens += item.Tokens
		totalGPUHours += item.GPUHours
	}

	// Sort breakdown for deterministic output.
	sort.Slice(breakdown, func(i, j int) bool {
		if breakdown[i].Model != breakdown[j].Model {
			return breakdown[i].Model < breakdown[j].Model
		}
		return breakdown[i].Cluster < breakdown[j].Cluster
	})

	report.CostBreakdown = breakdown
	report.TotalCost = totalCost
	report.TokensConsumed = totalTokens
	report.GPUHoursUsed = totalGPUHours

	if budget > 0 {
		report.BudgetUsed = (totalCost / budget) * 100.0
	}

	return report
}
