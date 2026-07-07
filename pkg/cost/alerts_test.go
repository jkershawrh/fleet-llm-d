package cost

import (
	"testing"
)

func TestCheckBudgetAlerts_NoAlert(t *testing.T) {
	configs := []TenantBudgetConfig{
		{TenantID: "tenant-1", MonthlyBudget: 1000, WarningAt: 0.8, CriticalAt: 0.95},
	}
	costs := map[string]float64{
		"tenant-1": 500, // 50% - below warning
	}

	alerts := CheckBudgetAlerts(configs, costs)
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts at 50%% budget, got %d", len(alerts))
	}
}

func TestCheckBudgetAlerts_Warning(t *testing.T) {
	configs := []TenantBudgetConfig{
		{TenantID: "tenant-1", MonthlyBudget: 1000, WarningAt: 0.8, CriticalAt: 0.95},
	}
	costs := map[string]float64{
		"tenant-1": 850, // 85% - above warning, below critical
	}

	alerts := CheckBudgetAlerts(configs, costs)

	// Should have exactly 1 alert: warning
	foundWarning := false
	for _, a := range alerts {
		if a.AlertLevel == "warning" {
			foundWarning = true
			if a.TenantID != "tenant-1" {
				t.Fatalf("warning alert tenant = %q, want 'tenant-1'", a.TenantID)
			}
			if a.Threshold != 0.8 {
				t.Fatalf("warning threshold = %f, want 0.8", a.Threshold)
			}
		}
		if a.AlertLevel == "critical" || a.AlertLevel == "exceeded" {
			t.Fatalf("unexpected %s alert at 85%% budget", a.AlertLevel)
		}
	}
	if !foundWarning {
		t.Fatal("expected warning alert at 85% budget, got none")
	}
}

func TestCheckBudgetAlerts_Critical(t *testing.T) {
	configs := []TenantBudgetConfig{
		{TenantID: "tenant-1", MonthlyBudget: 1000, WarningAt: 0.8, CriticalAt: 0.95},
	}
	costs := map[string]float64{
		"tenant-1": 960, // 96% - above critical, below exceeded
	}

	alerts := CheckBudgetAlerts(configs, costs)

	// Should have both warning and critical alerts.
	var hasWarning, hasCritical bool
	for _, a := range alerts {
		switch a.AlertLevel {
		case "warning":
			hasWarning = true
		case "critical":
			hasCritical = true
		case "exceeded":
			t.Fatal("unexpected exceeded alert at 96% budget")
		}
	}
	if !hasWarning {
		t.Fatal("expected warning alert at 96%")
	}
	if !hasCritical {
		t.Fatal("expected critical alert at 96%")
	}

	// Verify sort order: critical before warning.
	if len(alerts) >= 2 {
		if alerts[0].AlertLevel != "critical" {
			t.Fatalf("first alert should be critical, got %q", alerts[0].AlertLevel)
		}
		if alerts[1].AlertLevel != "warning" {
			t.Fatalf("second alert should be warning, got %q", alerts[1].AlertLevel)
		}
	}
}

func TestCheckBudgetAlerts_Exceeded(t *testing.T) {
	configs := []TenantBudgetConfig{
		{TenantID: "tenant-1", MonthlyBudget: 1000, WarningAt: 0.8, CriticalAt: 0.95},
	}
	costs := map[string]float64{
		"tenant-1": 1100, // 110% - exceeded
	}

	alerts := CheckBudgetAlerts(configs, costs)

	// Should have all three alert levels.
	levels := make(map[string]bool)
	for _, a := range alerts {
		levels[a.AlertLevel] = true
	}
	for _, level := range []string{"warning", "critical", "exceeded"} {
		if !levels[level] {
			t.Fatalf("expected %s alert at 110%% budget", level)
		}
	}

	// First alert should be exceeded (highest severity).
	if alerts[0].AlertLevel != "exceeded" {
		t.Fatalf("first alert should be 'exceeded', got %q", alerts[0].AlertLevel)
	}
}

func TestCheckBudgetAlerts_MultiTenant(t *testing.T) {
	configs := []TenantBudgetConfig{
		{TenantID: "tenant-a", MonthlyBudget: 1000, WarningAt: 0.8, CriticalAt: 0.95},
		{TenantID: "tenant-b", MonthlyBudget: 500, WarningAt: 0.8, CriticalAt: 0.95},
		{TenantID: "tenant-c", MonthlyBudget: 2000, WarningAt: 0.8, CriticalAt: 0.95},
	}
	costs := map[string]float64{
		"tenant-a": 100,  // 10% - no alert
		"tenant-b": 520,  // 104% - exceeded
		"tenant-c": 1700, // 85% - warning only
	}

	alerts := CheckBudgetAlerts(configs, costs)

	// tenant-a: no alerts
	// tenant-b: exceeded + critical + warning = 3 alerts
	// tenant-c: warning = 1 alert
	tenantAlerts := make(map[string][]string)
	for _, a := range alerts {
		tenantAlerts[a.TenantID] = append(tenantAlerts[a.TenantID], a.AlertLevel)
	}

	if _, ok := tenantAlerts["tenant-a"]; ok {
		t.Fatal("tenant-a should have no alerts at 10%")
	}

	bAlerts := tenantAlerts["tenant-b"]
	if len(bAlerts) != 3 {
		t.Fatalf("tenant-b should have 3 alerts (exceeded+critical+warning), got %d: %v", len(bAlerts), bAlerts)
	}

	cAlerts := tenantAlerts["tenant-c"]
	if len(cAlerts) != 1 {
		t.Fatalf("tenant-c should have 1 alert (warning), got %d: %v", len(cAlerts), cAlerts)
	}
	if cAlerts[0] != "warning" {
		t.Fatalf("tenant-c alert should be 'warning', got %q", cAlerts[0])
	}

	// Verify overall sort: exceeded first across all tenants.
	if len(alerts) > 0 && alerts[0].AlertLevel != "exceeded" {
		t.Fatalf("first alert overall should be 'exceeded', got %q", alerts[0].AlertLevel)
	}
}
