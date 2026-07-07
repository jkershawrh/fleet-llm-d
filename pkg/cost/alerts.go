package cost

import (
	"fmt"
	"sort"
)

// BudgetAlert represents a budget threshold breach for a tenant.
type BudgetAlert struct {
	TenantID   string  `json:"tenant_id"`
	AlertLevel string  `json:"alert_level"` // "warning", "critical", "exceeded"
	Threshold  float64 `json:"threshold"`   // configured threshold (0.8, 0.95, 1.0)
	CurrentCost float64 `json:"current_cost"`
	Budget     float64 `json:"budget"`
	Percent    float64 `json:"percent"`
	Message    string  `json:"message"`
}

// TenantBudgetConfig defines the budget and alert thresholds for a tenant.
type TenantBudgetConfig struct {
	TenantID      string  `json:"tenant_id"`
	MonthlyBudget float64 `json:"monthly_budget"`
	WarningAt     float64 `json:"warning_at"`  // default 0.8
	CriticalAt    float64 `json:"critical_at"` // default 0.95
}

// alertSeverityOrder maps alert levels to sort priority (lower = more severe).
var alertSeverityOrder = map[string]int{
	"exceeded": 0,
	"critical": 1,
	"warning":  2,
}

// CheckBudgetAlerts evaluates current costs against budget configurations and
// returns alerts for all triggered thresholds. Results are sorted by severity
// (exceeded first, then critical, then warning).
func CheckBudgetAlerts(configs []TenantBudgetConfig, currentCosts map[string]float64) []BudgetAlert {
	var alerts []BudgetAlert

	for _, cfg := range configs {
		cost, ok := currentCosts[cfg.TenantID]
		if !ok {
			continue
		}

		if cfg.MonthlyBudget <= 0 {
			continue
		}

		percent := cost / cfg.MonthlyBudget

		warningAt := cfg.WarningAt
		if warningAt <= 0 {
			warningAt = 0.8
		}
		criticalAt := cfg.CriticalAt
		if criticalAt <= 0 {
			criticalAt = 0.95
		}

		// Check exceeded (>= 100%)
		if percent >= 1.0 {
			alerts = append(alerts, BudgetAlert{
				TenantID:    cfg.TenantID,
				AlertLevel:  "exceeded",
				Threshold:   1.0,
				CurrentCost: cost,
				Budget:      cfg.MonthlyBudget,
				Percent:     percent * 100,
				Message:     fmt.Sprintf("tenant %s has exceeded budget: $%.2f / $%.2f (%.1f%%)", cfg.TenantID, cost, cfg.MonthlyBudget, percent*100),
			})
		}

		// Check critical
		if percent >= criticalAt {
			alerts = append(alerts, BudgetAlert{
				TenantID:    cfg.TenantID,
				AlertLevel:  "critical",
				Threshold:   criticalAt,
				CurrentCost: cost,
				Budget:      cfg.MonthlyBudget,
				Percent:     percent * 100,
				Message:     fmt.Sprintf("tenant %s is at critical budget level: $%.2f / $%.2f (%.1f%%)", cfg.TenantID, cost, cfg.MonthlyBudget, percent*100),
			})
		}

		// Check warning
		if percent >= warningAt {
			alerts = append(alerts, BudgetAlert{
				TenantID:    cfg.TenantID,
				AlertLevel:  "warning",
				Threshold:   warningAt,
				CurrentCost: cost,
				Budget:      cfg.MonthlyBudget,
				Percent:     percent * 100,
				Message:     fmt.Sprintf("tenant %s is approaching budget limit: $%.2f / $%.2f (%.1f%%)", cfg.TenantID, cost, cfg.MonthlyBudget, percent*100),
			})
		}
	}

	// Sort by severity (exceeded first).
	sort.Slice(alerts, func(i, j int) bool {
		si := alertSeverityOrder[alerts[i].AlertLevel]
		sj := alertSeverityOrder[alerts[j].AlertLevel]
		if si != sj {
			return si < sj
		}
		return alerts[i].TenantID < alerts[j].TenantID
	})

	return alerts
}
