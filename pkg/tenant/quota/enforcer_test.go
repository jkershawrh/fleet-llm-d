package quota

import (
	"context"
	"testing"
)

func newTestEnforcer() QuotaEnforcer {
	e := NewQuotaEnforcer().(*DefaultQuotaEnforcer)
	e.mu.Lock()
	e.profiles["tenant-1"] = &tenantProfile{tokenLimit: 1000, budgetCents: 1000}
	e.profiles["tenant-2"] = &tenantProfile{tokenLimit: 1000, budgetCents: 1000}
	e.profiles["tenant-3"] = &tenantProfile{tokenLimit: 1000, tokensUsed: 1000, budgetCents: 0}
	e.mu.Unlock()
	return e
}

func TestCheckQuota_Allowed(t *testing.T) {
	enforcer := newTestEnforcer()

	got, err := enforcer.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{
		TokensRequested: 100, Model: "llama-3", ClusterID: "cluster-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Allowed {
		t.Error("expected allowed")
	}
	if got.RemainingTokens != 1000 {
		t.Errorf("RemainingTokens = %d, want 1000", got.RemainingTokens)
	}
	if got.RemainingBudget != "$10.00" {
		t.Errorf("RemainingBudget = %q, want $10.00", got.RemainingBudget)
	}
}

func TestCheckQuota_TokenLimitExceeded(t *testing.T) {
	enforcer := newTestEnforcer()

	got, err := enforcer.CheckQuota(context.Background(), "tenant-2", QuotaCheckRequest{
		TokensRequested: 50000, Model: "llama-3", ClusterID: "cluster-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Allowed {
		t.Error("expected not allowed")
	}
	if got.Reason != "token limit exceeded: requested 50000 but only 1000 remaining" {
		t.Errorf("Reason = %q", got.Reason)
	}
}

func TestCheckQuota_BudgetExceeded(t *testing.T) {
	enforcer := newTestEnforcer()

	got, err := enforcer.CheckQuota(context.Background(), "tenant-3", QuotaCheckRequest{
		TokensRequested: 100, Model: "llama-3", ClusterID: "cluster-b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Allowed {
		t.Error("expected not allowed")
	}
	if got.Reason != "budget exceeded: tenant has no remaining budget" {
		t.Errorf("Reason = %q", got.Reason)
	}
	if got.RemainingBudget != "$0.00" {
		t.Errorf("RemainingBudget = %q, want $0.00", got.RemainingBudget)
	}
}

func TestCheckQuota_DoesNotDeductTokens(t *testing.T) {
	e := newTestEnforcer()

	result1, err := e.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !result1.Allowed {
		t.Fatal("first check should be allowed")
	}
	remaining1 := result1.RemainingTokens

	result2, err := e.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatal(err)
	}
	if result2.RemainingTokens != remaining1 {
		t.Errorf("CheckQuota changed remaining tokens: %d -> %d", remaining1, result2.RemainingTokens)
	}
}

func TestConsumeQuota_DeductsTokens(t *testing.T) {
	e := newTestEnforcer()
	ce := e.(*DefaultQuotaEnforcer)

	result1, err := ce.ConsumeQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !result1.Allowed {
		t.Fatal("consume should be allowed")
	}

	result2, err := e.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result2.RemainingTokens != result1.RemainingTokens {
		t.Errorf("after ConsumeQuota, remaining = %d, expected %d", result2.RemainingTokens, result1.RemainingTokens)
	}

	result3, err := ce.ConsumeQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result3.RemainingTokens >= result1.RemainingTokens {
		t.Error("ConsumeQuota should reduce remaining tokens")
	}
}

func TestCheckQuota_UnknownTenantGetsDefault(t *testing.T) {
	e := NewQuotaEnforcer()

	got, err := e.CheckQuota(context.Background(), "new-tenant", QuotaCheckRequest{TokensRequested: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Allowed {
		t.Error("unknown tenant should get default profile and be allowed")
	}
}

func TestRecordUsage(t *testing.T) {
	enforcer := NewQuotaEnforcer()
	err := enforcer.RecordUsage(context.Background(), "tenant-1", UsageRecord{
		TokensConsumed: 500, Model: "llama-3", ClusterID: "cluster-a", LatencyMs: 120, Cost: "$0.50",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
