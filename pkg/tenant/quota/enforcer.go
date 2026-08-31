package quota

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// QuotaCheckRequest represents a request to check quota availability for a tenant.
type QuotaCheckRequest struct {
	TokensRequested int64
	Model           string
	ClusterID       string
}

// QuotaCheckResult represents the result of a quota check.
type QuotaCheckResult struct {
	Allowed         bool
	RemainingTokens int64
	RemainingBudget string
	Reason          string
}

// UsageRecord represents a record of token usage by a tenant.
type UsageRecord struct {
	TokensConsumed int64
	Model          string
	ClusterID      string
	LatencyMs      int
	Cost           string
}

// QuotaEnforcer defines the interface for enforcing tenant quota limits.
type QuotaEnforcer interface {
	CheckQuota(ctx context.Context, tenantID string, request QuotaCheckRequest) (QuotaCheckResult, error)
	RecordUsage(ctx context.Context, tenantID string, usage UsageRecord) error
}

// tenantProfile holds the quota limits and current usage for a tenant.
type tenantProfile struct {
	tokenLimit      int64
	tokensUsed      int64
	concurrentLimit int64
	budgetCents     int64 // total budget in cents
	budgetSpent     int64 // spent budget in cents
	tokenWindow     time.Time
	budgetMonth     time.Time
}

// costPerToken is the cost in cents per token.
const costPerToken int64 = 1

// DefaultQuotaEnforcer is the concrete quota enforcer. Exported so callers
// can type-assert to access ConsumeQuota.
type DefaultQuotaEnforcer struct {
	mu       sync.Mutex
	profiles map[string]*tenantProfile
	records  []usageEntry
	repo     postgres.TenantRepository
	fallback FallbackConfig
	now      func() time.Time
}

// FallbackConfig controls bounded quota enforcement when a tenant repository
// is absent or has no record for the authenticated tenant.
type FallbackConfig struct {
	TokenLimitPerMinute int64 `json:"tokenLimitPerMinute"`
	ConcurrentLimit     int64 `json:"concurrentLimit"`
	MonthlyBudgetCents  int64 `json:"monthlyBudgetCents"`
}

func DefaultFallbackConfig() FallbackConfig {
	return FallbackConfig{TokenLimitPerMinute: 1000, ConcurrentLimit: 100, MonthlyBudgetCents: 1000}
}

type usageEntry struct {
	tenantID string
	record   UsageRecord
}

// NewQuotaEnforcer returns a new QuotaEnforcer instance.
// If a TenantRepository is provided, tenant profiles are loaded from the repo
// when not found in the local cache.
func NewQuotaEnforcer(repo ...postgres.TenantRepository) QuotaEnforcer {
	return NewQuotaEnforcerWithFallback(DefaultFallbackConfig(), repo...)
}

// NewQuotaEnforcerWithFallback configures quota limits for tenants that are
// not backed by durable repository state.
func NewQuotaEnforcerWithFallback(fallback FallbackConfig, repo ...postgres.TenantRepository) QuotaEnforcer {
	var r postgres.TenantRepository
	if len(repo) > 0 {
		r = repo[0]
	}
	defaults := DefaultFallbackConfig()
	if fallback.TokenLimitPerMinute <= 0 {
		fallback.TokenLimitPerMinute = defaults.TokenLimitPerMinute
	}
	if fallback.ConcurrentLimit <= 0 {
		fallback.ConcurrentLimit = defaults.ConcurrentLimit
	}
	if fallback.MonthlyBudgetCents <= 0 {
		fallback.MonthlyBudgetCents = defaults.MonthlyBudgetCents
	}
	return &DefaultQuotaEnforcer{
		profiles: make(map[string]*tenantProfile),
		repo:     r,
		fallback: fallback,
		now:      time.Now,
	}
}

func (e *DefaultQuotaEnforcer) newFallbackProfile() *tenantProfile {
	now := e.now().UTC()
	return &tenantProfile{
		tokenLimit: e.fallback.TokenLimitPerMinute, concurrentLimit: e.fallback.ConcurrentLimit,
		budgetCents: e.fallback.MonthlyBudgetCents,
		tokenWindow: now.Truncate(time.Minute), budgetMonth: monthStart(now),
	}
}

func monthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (e *DefaultQuotaEnforcer) resetLocalWindows(profile *tenantProfile) {
	now := e.now().UTC()
	window := now.Truncate(time.Minute)
	if !profile.tokenWindow.IsZero() && !profile.tokenWindow.Equal(window) {
		profile.tokensUsed = 0
		profile.tokenWindow = window
	}
	month := monthStart(now)
	if !profile.budgetMonth.IsZero() && !profile.budgetMonth.Equal(month) {
		profile.budgetSpent = 0
		profile.budgetMonth = month
	}
}

func (e *DefaultQuotaEnforcer) loadFromRepo(tenantID string) *tenantProfile {
	if e.repo == nil {
		return nil
	}
	tenant, err := e.repo.Get(context.Background(), tenantID)
	if err != nil || tenant == nil {
		return nil
	}
	profile := e.newFallbackProfile()
	if quotas := tenant.Quotas; quotas != nil {
		if v, ok := quotas["maxTokensPerMinute"].(float64); ok {
			profile.tokenLimit = int64(v)
		}
		if v, ok := quotas["maxConcurrentRequests"].(float64); ok {
			profile.concurrentLimit = int64(v)
		}
	}
	if cc := tenant.CostControl; cc != nil {
		if v, ok := cc["monthlyBudget"].(float64); ok {
			profile.budgetCents = int64(v * 100)
		}
	}
	e.profiles[tenantID] = profile
	return profile
}

func formatBudget(cents int64) string {
	if cents < 0 {
		cents = 0
	}
	return fmt.Sprintf("$%.2f", float64(cents)/100.0)
}

func (e *DefaultQuotaEnforcer) CheckQuota(_ context.Context, tenantID string, request QuotaCheckRequest) (QuotaCheckResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	profile, ok := e.profiles[tenantID]
	if !ok {
		profile = e.loadFromRepo(tenantID)
	}
	if profile == nil {
		profile = e.newFallbackProfile()
		e.profiles[tenantID] = profile
	}
	e.resetLocalWindows(profile)

	remainingTokens := profile.tokenLimit - profile.tokensUsed
	remainingBudgetCents := profile.budgetCents - profile.budgetSpent

	// Check budget first.
	if remainingBudgetCents <= 0 {
		return QuotaCheckResult{
			Allowed:         false,
			RemainingTokens: remainingTokens,
			RemainingBudget: formatBudget(remainingBudgetCents),
			Reason:          "budget exceeded: tenant has no remaining budget",
		}, nil
	}

	// Check token limit.
	if request.TokensRequested > remainingTokens {
		return QuotaCheckResult{
			Allowed:         false,
			RemainingTokens: remainingTokens,
			RemainingBudget: formatBudget(remainingBudgetCents),
			Reason:          fmt.Sprintf("token limit exceeded: requested %d but only %d remaining", request.TokensRequested, remainingTokens),
		}, nil
	}

	// Read-only: report current remaining without deducting
	return QuotaCheckResult{
		Allowed:         true,
		RemainingTokens: remainingTokens,
		RemainingBudget: formatBudget(remainingBudgetCents),
		Reason:          "",
	}, nil
}

// ConsumeQuota checks quota and deducts tokens and budget if allowed.
// Unlike CheckQuota (which is read-only), this method mutates tenant state.
func (e *DefaultQuotaEnforcer) ConsumeQuota(ctx context.Context, tenantID string, request QuotaCheckRequest) (QuotaCheckResult, error) {
	return e.ReserveQuota(ctx, tenantID, request)
}

// ReserveQuota atomically reserves a minute-window token allowance and one
// active request when the repository supports distributed quota coordination.
func (e *DefaultQuotaEnforcer) ReserveQuota(ctx context.Context, tenantID string, request QuotaCheckRequest) (QuotaCheckResult, error) {
	e.mu.Lock()
	profile, ok := e.profiles[tenantID]
	if !ok {
		profile = e.loadFromRepo(tenantID)
	}
	if profile == nil {
		profile = e.newFallbackProfile()
		e.profiles[tenantID] = profile
	}
	if shared, ok := e.repo.(postgres.TenantQuotaRepository); ok {
		tokenLimit, concurrentLimit := profile.tokenLimit, profile.concurrentLimit
		e.mu.Unlock()
		windowStart := time.Now().UTC().Truncate(time.Minute)
		budgetCost, budgetLimit := request.TokensRequested*costPerToken, profile.budgetCents
		reservation, err := shared.ReserveQuota(ctx, tenantID, windowStart, request.TokensRequested, tokenLimit, concurrentLimit, budgetCost, budgetLimit)
		if err != nil {
			return QuotaCheckResult{}, err
		}
		remaining := tokenLimit - reservation.TokensReserved
		remainingBudget := budgetLimit - reservation.BudgetSpent
		if !reservation.Allowed {
			reason := "concurrent request limit exceeded"
			if reservation.TokensReserved+request.TokensRequested > tokenLimit {
				reason = fmt.Sprintf("token limit exceeded: requested %d but only %d remaining", request.TokensRequested, remaining)
			} else if reservation.BudgetSpent+budgetCost > budgetLimit {
				reason = "budget exceeded: tenant has no remaining budget"
			}
			return QuotaCheckResult{Allowed: false, RemainingTokens: remaining, RemainingBudget: formatBudget(remainingBudget), Reason: reason}, nil
		}
		return QuotaCheckResult{Allowed: true, RemainingTokens: remaining, RemainingBudget: formatBudget(remainingBudget)}, nil
	}
	defer e.mu.Unlock()
	e.resetLocalWindows(profile)

	remainingTokens := profile.tokenLimit - profile.tokensUsed
	remainingBudgetCents := profile.budgetCents - profile.budgetSpent

	if remainingBudgetCents <= 0 {
		return QuotaCheckResult{
			Allowed:         false,
			RemainingTokens: remainingTokens,
			RemainingBudget: formatBudget(remainingBudgetCents),
			Reason:          "budget exceeded: tenant has no remaining budget",
		}, nil
	}

	if request.TokensRequested > remainingTokens {
		return QuotaCheckResult{
			Allowed:         false,
			RemainingTokens: remainingTokens,
			RemainingBudget: formatBudget(remainingBudgetCents),
			Reason:          fmt.Sprintf("token limit exceeded: requested %d but only %d remaining", request.TokensRequested, remainingTokens),
		}, nil
	}

	profile.tokensUsed += request.TokensRequested
	tokenCost := request.TokensRequested * costPerToken
	profile.budgetSpent += tokenCost

	newRemainingTokens := profile.tokenLimit - profile.tokensUsed
	newRemainingBudgetCents := profile.budgetCents - profile.budgetSpent

	return QuotaCheckResult{
		Allowed:         true,
		RemainingTokens: newRemainingTokens,
		RemainingBudget: formatBudget(newRemainingBudgetCents),
		Reason:          "",
	}, nil
}

// ReleaseQuota releases the active-request reservation. Token reservations
// remain until the minute window expires.
func (e *DefaultQuotaEnforcer) ReleaseQuota(ctx context.Context, tenantID string) error {
	shared, ok := e.repo.(postgres.TenantQuotaRepository)
	if !ok {
		return nil
	}
	return shared.ReleaseQuota(ctx, tenantID, time.Now().UTC().Truncate(time.Minute))
}

func (e *DefaultQuotaEnforcer) RecordUsage(_ context.Context, tenantID string, usage UsageRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.records = append(e.records, usageEntry{
		tenantID: tenantID,
		record:   usage,
	})

	return nil
}
