package quota

import (
	"context"
	"testing"
)

func TestCheckQuota_Allowed(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		request  QuotaCheckRequest
		want     QuotaCheckResult
	}{
		{
			name:     "tenant with remaining quota is allowed",
			tenantID: "tenant-1",
			request: QuotaCheckRequest{
				TokensRequested: 100,
				Model:           "llama-3",
				ClusterID:       "cluster-a",
			},
			want: QuotaCheckResult{
				Allowed:         true,
				RemainingTokens: 900,
				RemainingBudget: "$9.00",
				Reason:          "",
			},
		},
	}

	enforcer := NewQuotaEnforcer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := enforcer.CheckQuota(context.Background(), tt.tenantID, tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Allowed != tt.want.Allowed {
				t.Errorf("Allowed = %v, want %v", got.Allowed, tt.want.Allowed)
			}
			if got.RemainingTokens != tt.want.RemainingTokens {
				t.Errorf("RemainingTokens = %d, want %d", got.RemainingTokens, tt.want.RemainingTokens)
			}
			if got.RemainingBudget != tt.want.RemainingBudget {
				t.Errorf("RemainingBudget = %q, want %q", got.RemainingBudget, tt.want.RemainingBudget)
			}
		})
	}
}

func TestCheckQuota_TokenLimitExceeded(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		request  QuotaCheckRequest
		want     QuotaCheckResult
	}{
		{
			name:     "request exceeds token limit",
			tenantID: "tenant-2",
			request: QuotaCheckRequest{
				TokensRequested: 50000,
				Model:           "llama-3",
				ClusterID:       "cluster-a",
			},
			want: QuotaCheckResult{
				Allowed:         false,
				RemainingTokens: 1000,
				RemainingBudget: "$10.00",
				Reason:          "token limit exceeded: requested 50000 but only 1000 remaining",
			},
		},
	}

	enforcer := NewQuotaEnforcer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := enforcer.CheckQuota(context.Background(), tt.tenantID, tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Allowed != tt.want.Allowed {
				t.Errorf("Allowed = %v, want %v", got.Allowed, tt.want.Allowed)
			}
			if got.Reason != tt.want.Reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.want.Reason)
			}
		})
	}
}

func TestCheckQuota_BudgetExceeded(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		request  QuotaCheckRequest
		want     QuotaCheckResult
	}{
		{
			name:     "tenant over budget",
			tenantID: "tenant-3",
			request: QuotaCheckRequest{
				TokensRequested: 100,
				Model:           "llama-3",
				ClusterID:       "cluster-b",
			},
			want: QuotaCheckResult{
				Allowed:         false,
				RemainingTokens: 0,
				RemainingBudget: "$0.00",
				Reason:          "budget exceeded: tenant has no remaining budget",
			},
		},
	}

	enforcer := NewQuotaEnforcer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := enforcer.CheckQuota(context.Background(), tt.tenantID, tt.request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Allowed != tt.want.Allowed {
				t.Errorf("Allowed = %v, want %v", got.Allowed, tt.want.Allowed)
			}
			if got.Reason != tt.want.Reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.want.Reason)
			}
			if got.RemainingBudget != tt.want.RemainingBudget {
				t.Errorf("RemainingBudget = %q, want %q", got.RemainingBudget, tt.want.RemainingBudget)
			}
		})
	}
}

func TestRecordUsage(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		usage    UsageRecord
	}{
		{
			name:     "records usage successfully",
			tenantID: "tenant-1",
			usage: UsageRecord{
				TokensConsumed: 500,
				Model:          "llama-3",
				ClusterID:      "cluster-a",
				LatencyMs:      120,
				Cost:           "$0.50",
			},
		},
	}

	enforcer := NewQuotaEnforcer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enforcer.RecordUsage(context.Background(), tt.tenantID, tt.usage)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
