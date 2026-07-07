//go:build bdd

package steps

import (
	"fmt"
	"math"
)

// FederateMetrics runs the metrics federator and stores results.
func (w *World) FederateMetrics(clusters []string) error {
	fm, err := w.Federator.FederateMetrics(w.Ctx, clusters)
	if err != nil {
		w.LastError = err
		return nil
	}
	_ = fm // Store for later assertions
	return nil
}

// GetModelMetrics retrieves model-specific metrics.
func (w *World) GetModelMetrics(model string) error {
	mm, err := w.Federator.GetModelMetrics(w.Ctx, model)
	if err != nil {
		w.LastError = err
		return nil
	}
	_ = mm
	return nil
}

// GetTenantMetrics retrieves tenant-specific metrics.
func (w *World) GetTenantMetrics(tenantID string) error {
	tm, err := w.Federator.GetTenantMetrics(w.Ctx, tenantID)
	if err != nil {
		w.LastError = err
		return nil
	}
	_ = tm
	return nil
}

// SLOComplianceEntry represents an SLO check for a model on a cluster.
type SLOComplianceEntry struct {
	Cluster     string
	Model       string
	TTFTP99Ms   float64
	SuccessRate float64
	TargetTTFT  float64
	TargetRate  float64
	Compliant   bool
	Reason      string
}

// CheckSLOCompliance evaluates SLO compliance for all model-cluster pairs.
func (w *World) CheckSLOCompliance(entries []SLOComplianceEntry) (compliant int, breaching int, results []SLOComplianceEntry) {
	for _, e := range entries {
		entry := e
		entry.Compliant = true
		entry.Reason = ""

		if entry.TTFTP99Ms > entry.TargetTTFT {
			entry.Compliant = false
			entry.Reason = fmt.Sprintf("ttft_p99 %.0fms > %.0fms target", entry.TTFTP99Ms, entry.TargetTTFT)
		}
		if entry.SuccessRate < entry.TargetRate {
			entry.Compliant = false
			if entry.Reason != "" {
				entry.Reason += "; "
			}
			entry.Reason += fmt.Sprintf("success_rate %.3f < %.3f target", entry.SuccessRate, entry.TargetRate)
		}

		if entry.Compliant {
			compliant++
		} else {
			breaching++
		}
		results = append(results, entry)
	}
	return
}

// ComputeFleetSLOComplianceRate computes the overall SLO compliance percentage.
func ComputeFleetSLOComplianceRate(compliant, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(compliant)/float64(total)*1000) / 10.0
}

// ComputeTenantCost calculates cost from GPU hours and rate.
func ComputeTenantCost(gpuHours, costPerGPUHour float64) float64 {
	return math.Round(gpuHours*costPerGPUHour*100) / 100.0
}

// ComputeBudgetUtilization calculates budget utilization percentage.
func ComputeBudgetUtilization(cost, budget float64) float64 {
	if budget == 0 {
		return 0
	}
	return math.Round(cost/budget*1000) / 10.0
}
