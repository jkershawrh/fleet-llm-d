package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/cost"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/metering"
)

// handleCostPricing returns the full GPU pricing table as JSON.
func (fc *FleetController) handleCostPricing(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.Add(1)
	writeJSON(w, http.StatusOK, fc.PricingTable.AllPrices())
}

// handleCostTokenomics computes token costs for a model across all GPU types.
func (fc *FleetController) handleCostTokenomics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	model := r.PathValue("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}

	throughput := 1000.0
	if tpStr := r.URL.Query().Get("throughput"); tpStr != "" {
		tp, err := strconv.ParseFloat(tpStr, 64)
		if err != nil || tp <= 0 {
			writeError(w, http.StatusBadRequest, "throughput must be a positive number")
			return
		}
		throughput = tp
	}

	var results []cost.TokenCost
	for _, gpuType := range fc.PricingTable.ListGPUTypes() {
		for _, tier := range fc.PricingTable.ListTiers() {
			tc, err := cost.ComputeTokenCost(model, gpuType, tier, throughput, fc.PricingTable)
			if err != nil {
				continue
			}
			results = append(results, *tc)
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// handleCostChargeback generates a chargeback report for a tenant.
func (fc *FleetController) handleCostChargeback(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	tenant := r.PathValue("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant id is required")
		return
	}

	budget := 10000.0
	if bStr := r.URL.Query().Get("budget"); bStr != "" {
		b, err := strconv.ParseFloat(bStr, 64)
		if err == nil && b > 0 {
			budget = b
		}
	}

	// Build usage records from the metering summary. Since the metering
	// system returns aggregate summaries rather than individual records, we
	// construct a single synthetic usage record per tenant.
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	period := metering.TimePeriod{Start: start, End: now}

	var costUsage []cost.UsageRecord
	meterUsage, err := fc.UsageTracker.GetUsage(r.Context(), tenant, period)
	if err == nil && meterUsage != nil {
		costUsage = append(costUsage, cost.UsageRecord{
			TenantID:  tenant,
			Model:     "aggregate",
			Cluster:   "fleet",
			GPUType:   "H200",
			Tokens:    meterUsage.TokensConsumed,
			Duration:  time.Duration(meterUsage.RequestCount) * time.Second,
			Timestamp: now,
		})
	}

	report := cost.GenerateChargebackReport(tenant, costUsage, fc.PricingTable, budget)
	writeJSON(w, http.StatusOK, report)
}

// handleCostProjection projects monthly cost based on current usage rates.
func (fc *FleetController) handleCostProjection(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	tokensPerDay := int64(10_000_000) // default
	if tdStr := r.URL.Query().Get("tokens_per_day"); tdStr != "" {
		td, err := strconv.ParseInt(tdStr, 10, 64)
		if err != nil || td <= 0 {
			writeError(w, http.StatusBadRequest, "tokens_per_day must be a positive integer")
			return
		}
		tokensPerDay = td
	}

	gpuType := r.URL.Query().Get("gpu_type")
	if gpuType == "" {
		gpuType = "H200"
	}
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "on-demand"
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		model = "default"
	}

	tc, err := cost.ComputeTokenCost(model, gpuType, tier, 1000, fc.PricingTable)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	monthly := cost.ProjectMonthlyCost(tokensPerDay, tc)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"model":          model,
		"gpu_type":       gpuType,
		"tier":           tier,
		"tokens_per_day": tokensPerDay,
		"monthly_cost":   monthly,
		"token_cost":     tc,
	})
}

// handleCostSavings compares current cost versus optimized placement cost.
func (fc *FleetController) handleCostSavings(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	currentMonthly := 5000.0
	if cmStr := r.URL.Query().Get("current"); cmStr != "" {
		cm, err := strconv.ParseFloat(cmStr, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "current must be a number")
			return
		}
		currentMonthly = cm
	}

	optimizedMonthly := 3000.0
	if omStr := r.URL.Query().Get("optimized"); omStr != "" {
		om, err := strconv.ParseFloat(omStr, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "optimized must be a number")
			return
		}
		optimizedMonthly = om
	}

	projection := cost.ProjectSavings(currentMonthly, optimizedMonthly)
	writeJSON(w, http.StatusOK, projection)
}

// handleCostAlerts checks all tenant budgets and returns active alerts.
func (fc *FleetController) handleCostAlerts(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	tenants, err := fc.TenantRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var configs []cost.TenantBudgetConfig
	currentCosts := make(map[string]float64)

	for _, t := range tenants {
		// Extract budget from CostControl if available; default to $10,000.
		budget := 10000.0
		if t.CostControl != nil {
			if b, ok := t.CostControl["monthly_budget"]; ok {
				if bf, ok := b.(float64); ok {
					budget = bf
				}
			}
		}
		configs = append(configs, cost.TenantBudgetConfig{
			TenantID:      t.ID,
			MonthlyBudget: budget,
			WarningAt:     0.8,
			CriticalAt:    0.95,
		})
		// Estimate current cost from metering data.
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		period := metering.TimePeriod{Start: start, End: now}
		usage, err := fc.UsageTracker.GetUsage(r.Context(), t.ID, period)
		if err == nil && usage != nil {
			// Parse cost string from metering summary.
			if costVal, parseErr := strconv.ParseFloat(usage.TotalCost, 64); parseErr == nil {
				currentCosts[t.ID] = costVal
			}
		}
	}

	alerts := cost.CheckBudgetAlerts(configs, currentCosts)
	writeJSON(w, http.StatusOK, alerts)
}
