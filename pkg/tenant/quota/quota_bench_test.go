package quota

import (
	"context"
	"fmt"
	"testing"
)

func benchmarkCheckQuota(b *testing.B, tenantCount int) {
	enforcer := NewQuotaEnforcer().(*DefaultQuotaEnforcer)
	ctx := context.Background()

	enforcer.mu.Lock()
	for i := 0; i < tenantCount; i++ {
		enforcer.profiles[fmt.Sprintf("tenant-%d", i)] = &tenantProfile{
			tokenLimit:  1000000,
			tokensUsed:  0,
			budgetCents: 100000,
			budgetSpent: 0,
		}
	}
	enforcer.mu.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tenantID := fmt.Sprintf("tenant-%d", i%tenantCount)
		_, _ = enforcer.CheckQuota(ctx, tenantID, QuotaCheckRequest{
			TokensRequested: 100,
			Model:           "granite-3b",
		})
	}
}

func BenchmarkCheckQuota_10(b *testing.B)   { benchmarkCheckQuota(b, 10) }
func BenchmarkCheckQuota_50(b *testing.B)   { benchmarkCheckQuota(b, 50) }
func BenchmarkCheckQuota_100(b *testing.B)  { benchmarkCheckQuota(b, 100) }
func BenchmarkCheckQuota_250(b *testing.B)  { benchmarkCheckQuota(b, 250) }
func BenchmarkCheckQuota_500(b *testing.B)  { benchmarkCheckQuota(b, 500) }
func BenchmarkCheckQuota_1000(b *testing.B) { benchmarkCheckQuota(b, 1000) }
