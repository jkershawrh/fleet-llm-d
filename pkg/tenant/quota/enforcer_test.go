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
				RemainingTokens: 1000,
				RemainingBudget: "$10.00",
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

func TestCheckQuota_DoesNotDeductTokens(t *testing.T) {
	e := NewQuotaEnforcer()

	// First check — should be allowed
	result1, err := e.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !result1.Allowed {
		t.Fatal("first check should be allowed")
	}
	remaining1 := result1.RemainingTokens

	// Second check with same amount — remaining should be unchanged
	result2, err := e.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !result2.Allowed {
		t.Fatal("second check should be allowed")
	}

	if result2.RemainingTokens != remaining1 {
		t.Errorf("CheckQuota changed remaining tokens: %d -> %d (should be read-only)", remaining1, result2.RemainingTokens)
	}
}

func TestConsumeQuota_DeductsTokens(t *testing.T) {
	e := NewQuotaEnforcer()
	ce, ok := e.(*DefaultQuotaEnforcer)
	if !ok {
		t.Skip("not DefaultQuotaEnforcer")
	}

	// Consume 500 tokens
	result1, err := ce.ConsumeQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !result1.Allowed {
		t.Fatal("consume should be allowed")
	}

	// Check should show reduced tokens
	result2, err := e.CheckQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result2.RemainingTokens != result1.RemainingTokens {
		t.Errorf("after ConsumeQuota, CheckQuota should see reduced tokens: got %d, expected %d",
			result2.RemainingTokens, result1.RemainingTokens)
	}

	// Consume should show further reduction
	result3, err := ce.ConsumeQuota(context.Background(), "tenant-1", QuotaCheckRequest{TokensRequested: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result3.RemainingTokens >= result1.RemainingTokens {
		t.Error("ConsumeQuota should reduce remaining tokens each call")
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
