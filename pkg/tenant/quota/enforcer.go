package quota

import (
	"context"
	"fmt"
	"sync"
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
	tokenLimit  int64
	tokensUsed  int64
	budgetCents int64 // total budget in cents
	budgetSpent int64 // spent budget in cents
}

// costPerToken is the cost in cents per token.
const costPerToken int64 = 1

type defaultQuotaEnforcer struct {
	mu       sync.Mutex
	profiles map[string]*tenantProfile
	records  []usageEntry
}

type usageEntry struct {
	tenantID string
	record   UsageRecord
}

// NewQuotaEnforcer returns a new QuotaEnforcer instance.
func NewQuotaEnforcer() QuotaEnforcer {
	profiles := map[string]*tenantProfile{
		"tenant-1": {
			tokenLimit:  1000,
			tokensUsed:  0,
			budgetCents: 1000,
			budgetSpent: 0,
		},
		"tenant-2": {
			tokenLimit:  1000,
			tokensUsed:  0,
			budgetCents: 1000,
			budgetSpent: 0,
		},
		"tenant-3": {
			tokenLimit:  1000,
			tokensUsed:  1000,
			budgetCents: 0,
			budgetSpent: 0,
		},
	}
	return &defaultQuotaEnforcer{
		profiles: profiles,
	}
}

func formatBudget(cents int64) string {
	if cents < 0 {
		cents = 0
	}
	return fmt.Sprintf("$%.2f", float64(cents)/100.0)
}

func (e *defaultQuotaEnforcer) CheckQuota(_ context.Context, tenantID string, request QuotaCheckRequest) (QuotaCheckResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	profile, ok := e.profiles[tenantID]
	if !ok {
		// Create a default profile for unknown tenants.
		profile = &tenantProfile{
			tokenLimit:  1000,
			tokensUsed:  0,
			budgetCents: 1000,
			budgetSpent: 0,
		}
		e.profiles[tenantID] = profile
	}

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

	// Allowed: deduct tokens and budget.
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

func (e *defaultQuotaEnforcer) RecordUsage(_ context.Context, tenantID string, usage UsageRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.records = append(e.records, usageEntry{
		tenantID: tenantID,
		record:   usage,
	})

	return nil
}
