//go:build bdd

package steps

import (
	"fmt"
	"strconv"

	"github.com/llm-d/fleet-llm-d/pkg/tenant/quota"
)

// RegisterTenant creates a tenant in the world.
func (w *World) RegisterTenant(name string, maxTokensPerMinute int64, maxConcurrent int, priority int) {
	w.Tenants[name] = &TenantState{
		TokensConsumed: 0,
		Priority:       priority,
	}
}

// SetTenantTokensConsumed sets how many tokens a tenant has consumed.
func (w *World) SetTenantTokensConsumed(name string, tokens int64) {
	if t, ok := w.Tenants[name]; ok {
		t.TokensConsumed = tokens
	}
}

// SetTenantCost sets the accumulated cost for a tenant.
func (w *World) SetTenantCost(name string, cost float64) {
	if t, ok := w.Tenants[name]; ok {
		t.CostAccumulated = cost
	}
}

// SetTenantAllowedClusters sets which clusters a tenant can use.
func (w *World) SetTenantAllowedClusters(name string, clusters []string) {
	if t, ok := w.Tenants[name]; ok {
		t.AllowedClusters = clusters
	}
}

// CheckTenantQuota runs a quota check for a tenant.
func (w *World) CheckTenantQuota(tenantID string, tokensRequested int64) error {
	req := quota.QuotaCheckRequest{
		TokensRequested: tokensRequested,
		Model:           "default-model",
		ClusterID:       "default-cluster",
	}

	result, err := w.QuotaEnforcer.CheckQuota(w.Ctx, tenantID, req)
	if err != nil {
		w.LastError = err
		return nil
	}

	w.LastQuotaResult = &result
	w.LastError = nil
	return nil
}

// AssertQuotaAllowed checks that the last quota check was allowed.
func (w *World) AssertQuotaAllowed() error {
	if w.LastQuotaResult == nil {
		return fmt.Errorf("no quota result available")
	}
	if !w.LastQuotaResult.Allowed {
		return fmt.Errorf("expected quota to be allowed, but was denied: %s", w.LastQuotaResult.Reason)
	}
	return nil
}

// AssertQuotaDenied checks that the last quota check was denied.
func (w *World) AssertQuotaDenied() error {
	if w.LastQuotaResult == nil {
		return fmt.Errorf("no quota result available")
	}
	if w.LastQuotaResult.Allowed {
		return fmt.Errorf("expected quota to be denied, but was allowed")
	}
	return nil
}

// AssertRemainingTokens checks the remaining tokens in the quota result.
func (w *World) AssertRemainingTokens(expected int64) error {
	if w.LastQuotaResult == nil {
		return fmt.Errorf("no quota result available")
	}
	if w.LastQuotaResult.RemainingTokens != expected {
		return fmt.Errorf("expected %d remaining tokens, got %d", expected, w.LastQuotaResult.RemainingTokens)
	}
	return nil
}

// AssertRemainingBudget checks the remaining budget string.
func (w *World) AssertRemainingBudget(expected string) error {
	if w.LastQuotaResult == nil {
		return fmt.Errorf("no quota result available")
	}
	if w.LastQuotaResult.RemainingBudget != expected {
		return fmt.Errorf("expected remaining budget %q, got %q", expected, w.LastQuotaResult.RemainingBudget)
	}
	return nil
}

// RecordTenantUsage records usage for metering.
func (w *World) RecordTenantUsage(tenantID, model, cluster string, inputTokens, outputTokens int64) error {
	totalTokens := inputTokens + outputTokens
	usage := quota.UsageRecord{
		TokensConsumed: totalTokens,
		Model:          model,
		ClusterID:      cluster,
		LatencyMs:      100,
		Cost:           fmt.Sprintf("%.2f", float64(totalTokens)*0.001),
	}

	err := w.QuotaEnforcer.RecordUsage(w.Ctx, tenantID, usage)
	if err != nil {
		return fmt.Errorf("failed to record usage: %w", err)
	}

	// Also track in our world state
	if t, ok := w.Tenants[tenantID]; ok {
		t.TokensConsumed += totalTokens
	}
	return nil
}

// RecordTenantUsageLedger records usage in the immutable ledger.
func (w *World) RecordTenantUsageLedger(tenantID, cluster string, tokens int64) error {
	cost := fmt.Sprintf("%.2f", float64(tokens)*0.001)
	receipt, err := w.Recorder.RecordTenantUsage(w.Ctx, tenantID, "default-model", cluster, tokens, cost)
	if err != nil {
		w.LastError = err
		return nil
	}

	w.LedgerEntries = append(w.LedgerEntries, LedgerEntry{
		Type:    "fleet.tenant.usage",
		Receipt: receipt,
	})
	return nil
}

// AssertLedgerEntryExists checks that a ledger entry of the given type exists.
func (w *World) AssertLedgerEntryExists(entryType string) error {
	for _, entry := range w.LedgerEntries {
		if entry.Type == entryType {
			return nil
		}
	}
	return fmt.Errorf("no ledger entry of type %q found", entryType)
}

// AssertLedgerChainValid checks that ledger entries have valid chain positions.
func (w *World) AssertLedgerChainValid() error {
	if len(w.LedgerEntries) == 0 {
		return fmt.Errorf("no ledger entries to verify")
	}
	// Verify chain positions are in order
	for i := 1; i < len(w.LedgerEntries); i++ {
		if w.LedgerEntries[i].Receipt != nil && w.LedgerEntries[i-1].Receipt != nil {
			if w.LedgerEntries[i].Receipt.ChainPosition <= w.LedgerEntries[i-1].Receipt.ChainPosition {
				return fmt.Errorf("chain positions not strictly increasing at index %d", i)
			}
		}
	}
	return nil
}

// AssertTenantTokensConsumed checks the total tokens consumed for a tenant.
func (w *World) AssertTenantTokensConsumed(tenantID string, expected int64) error {
	t, ok := w.Tenants[tenantID]
	if !ok {
		return fmt.Errorf("tenant %q not found", tenantID)
	}
	if t.TokensConsumed != expected {
		return fmt.Errorf("expected %d tokens consumed, got %d", expected, t.TokensConsumed)
	}
	return nil
}

// ParseTokenCount parses a token count string to int64.
func ParseTokenCount(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
