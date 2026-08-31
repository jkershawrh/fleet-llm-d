package quota

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

type sharedQuotaTestRepository struct {
	postgres.TenantRepository
	mu         sync.Mutex
	tokens     int64
	concurrent int64
	budget     int64
	tokenLimit float64
	budgetUSD  float64
}

func (r *sharedQuotaTestRepository) Get(_ context.Context, id string) (*postgres.TenantRecord, error) {
	tokenLimit := r.tokenLimit
	if tokenLimit == 0 {
		tokenLimit = 10
	}
	budgetUSD := r.budgetUSD
	if budgetUSD == 0 {
		budgetUSD = 10
	}
	return &postgres.TenantRecord{ID: id, Quotas: map[string]interface{}{
		"maxTokensPerMinute":    tokenLimit,
		"maxConcurrentRequests": float64(1),
	}, CostControl: map[string]interface{}{"monthlyBudget": budgetUSD}}, nil
}

func (r *sharedQuotaTestRepository) ReserveQuota(_ context.Context, _ string, _ time.Time, tokens, tokenLimit, concurrentLimit, budgetCost, budgetLimit int64) (postgres.QuotaReservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := postgres.QuotaReservation{TokensReserved: r.tokens, ActiveRequests: r.concurrent, BudgetSpent: r.budget}
	if r.tokens+tokens > tokenLimit || r.concurrent >= concurrentLimit || r.budget+budgetCost > budgetLimit {
		return result, nil
	}
	r.tokens += tokens
	r.concurrent++
	r.budget += budgetCost
	result.Allowed = true
	result.TokensReserved = r.tokens
	result.ActiveRequests = r.concurrent
	result.BudgetSpent = r.budget
	return result, nil
}

func (r *sharedQuotaTestRepository) ReleaseQuota(_ context.Context, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.concurrent > 0 {
		r.concurrent--
	}
	return nil
}

func newTestEnforcer() QuotaEnforcer {
	e := NewQuotaEnforcer().(*DefaultQuotaEnforcer)
	e.mu.Lock()
	e.profiles["tenant-1"] = &tenantProfile{tokenLimit: 1000, budgetCents: 1000}
	e.profiles["tenant-2"] = &tenantProfile{tokenLimit: 1000, budgetCents: 1000}
	e.profiles["tenant-3"] = &tenantProfile{tokenLimit: 1000, tokensUsed: 1000, budgetCents: 0}
	e.mu.Unlock()
	return e
}

func TestDistributedReservationSharedAcrossEnforcers(t *testing.T) {
	repo := &sharedQuotaTestRepository{}
	first := NewQuotaEnforcer(repo).(*DefaultQuotaEnforcer)
	second := NewQuotaEnforcer(repo).(*DefaultQuotaEnforcer)

	result, err := first.ReserveQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 6})
	if err != nil || !result.Allowed {
		t.Fatalf("first reservation = (%+v, %v), want allowed", result, err)
	}
	result, err = second.ReserveQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 1})
	if err != nil || result.Allowed || result.Reason != "concurrent request limit exceeded" {
		t.Fatalf("concurrent reservation = (%+v, %v), want concurrency denial", result, err)
	}
	if err := first.ReleaseQuota(context.Background(), "tenant-1"); err != nil {
		t.Fatal(err)
	}
	result, err = second.ReserveQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 5})
	if err != nil || result.Allowed {
		t.Fatalf("over-limit reservation = (%+v, %v), want token denial", result, err)
	}
	result, err = second.ReserveQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 4})
	if err != nil || !result.Allowed || result.RemainingTokens != 0 {
		t.Fatalf("final reservation = (%+v, %v), want allowed with zero remaining", result, err)
	}
}

func TestDistributedReservationEnforcesSharedBudget(t *testing.T) {
	repo := &sharedQuotaTestRepository{tokenLimit: 100, budgetUSD: 0.05}
	enforcer := NewQuotaEnforcer(repo).(*DefaultQuotaEnforcer)
	result, err := enforcer.ReserveQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 6})
	if err != nil || result.Allowed || result.Reason != "budget exceeded: tenant has no remaining budget" {
		t.Fatalf("budget reservation = (%+v, %v), want budget denial", result, err)
	}
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

func TestFallbackQuotaResetsTokenWindowButNotMonthlyBudget(t *testing.T) {
	now := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	e := NewQuotaEnforcerWithFallback(FallbackConfig{
		TokenLimitPerMinute: 8, ConcurrentLimit: 1, MonthlyBudgetCents: 100,
	}).(*DefaultQuotaEnforcer)
	e.now = func() time.Time { return now }

	first, err := e.ReserveQuota(context.Background(), "certification", QuotaCheckRequest{TokensRequested: 8})
	if err != nil || !first.Allowed {
		t.Fatalf("first reservation = (%+v, %v), want allowed", first, err)
	}
	denied, err := e.ReserveQuota(context.Background(), "certification", QuotaCheckRequest{TokensRequested: 1})
	if err != nil || denied.Allowed {
		t.Fatalf("same-window reservation = (%+v, %v), want denied", denied, err)
	}

	now = now.Add(time.Minute)
	reset, err := e.ReserveQuota(context.Background(), "certification", QuotaCheckRequest{TokensRequested: 8})
	if err != nil || !reset.Allowed || reset.RemainingTokens != 0 {
		t.Fatalf("next-window reservation = (%+v, %v), want allowed", reset, err)
	}
	if reset.RemainingBudget != "$0.84" {
		t.Fatalf("monthly budget = %s, want $0.84", reset.RemainingBudget)
	}
}

func TestFallbackQuotaResetsMonthlyBudget(t *testing.T) {
	now := time.Date(2026, time.August, 31, 23, 59, 0, 0, time.UTC)
	e := NewQuotaEnforcerWithFallback(FallbackConfig{
		TokenLimitPerMinute: 10, ConcurrentLimit: 1, MonthlyBudgetCents: 8,
	}).(*DefaultQuotaEnforcer)
	e.now = func() time.Time { return now }
	if result, err := e.ReserveQuota(context.Background(), "certification", QuotaCheckRequest{TokensRequested: 8}); err != nil || !result.Allowed {
		t.Fatalf("August reservation = (%+v, %v), want allowed", result, err)
	}
	now = now.Add(time.Minute)
	result, err := e.ReserveQuota(context.Background(), "certification", QuotaCheckRequest{TokensRequested: 8})
	if err != nil || !result.Allowed || result.RemainingBudget != "$0.00" {
		t.Fatalf("September reservation = (%+v, %v), want reset budget", result, err)
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
