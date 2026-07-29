package ledger

import (
	"context"
	"fmt"
	"testing"
)

func benchmarkRecordPlacement(b *testing.B, prePopulate int) {
	client := NewInMemoryLedgerClient()
	recorder := NewFleetRecorder(client, "bench-controller", "bench-fleet")
	ctx := context.Background()

	for i := 0; i < prePopulate; i++ {
		_, _ = recorder.RecordPlacement(ctx, fmt.Sprintf("model-%d", i), fmt.Sprintf("cluster-%d", i), 1, "H100", "bench setup")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = recorder.RecordPlacement(ctx, "bench-model", fmt.Sprintf("cluster-%d", i%100), 1, "H100", "bench")
	}
}

func benchmarkVerifyAllChains(b *testing.B, entryCount int) {
	client := NewInMemoryLedgerClient()
	recorder := NewFleetRecorder(client, "bench-controller", "bench-fleet")
	ctx := context.Background()

	for i := 0; i < entryCount; i++ {
		_, _ = recorder.RecordPlacement(ctx, fmt.Sprintf("model-%d", i%10), fmt.Sprintf("cluster-%d", i%50), 1, "H100", "populate")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = recorder.VerifyAllChains(ctx)
	}
}

func BenchmarkRecordPlacement_10(b *testing.B)   { benchmarkRecordPlacement(b, 10) }
func BenchmarkRecordPlacement_100(b *testing.B)  { benchmarkRecordPlacement(b, 100) }
func BenchmarkRecordPlacement_1000(b *testing.B) { benchmarkRecordPlacement(b, 1000) }

func BenchmarkVerifyAllChains_10(b *testing.B)   { benchmarkVerifyAllChains(b, 10) }
func BenchmarkVerifyAllChains_100(b *testing.B)  { benchmarkVerifyAllChains(b, 100) }
func BenchmarkVerifyAllChains_1000(b *testing.B) { benchmarkVerifyAllChains(b, 1000) }
