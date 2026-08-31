package transfer

import (
	"context"
	"fmt"
	"testing"
)

func benchmarkInitiateTransfer(b *testing.B, n int) {
	orch := NewTransferOrchestrator()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = orch.InitiateTransfer(ctx, TransferRequest{
			SourceCluster:    fmt.Sprintf("source-%d", i%n),
			TargetCluster:    fmt.Sprintf("target-%d", i%n),
			Model:            "granite-3b",
			MaxBandwidthMbps: 1000,
		})
	}
}

func benchmarkGetTransferStatus(b *testing.B, n int) {
	orch := NewTransferOrchestrator()
	ctx := context.Background()

	jobIDs := make([]string, n)
	for i := 0; i < n; i++ {
		job, _ := orch.InitiateTransfer(ctx, TransferRequest{
			SourceCluster:    fmt.Sprintf("source-%d", i),
			TargetCluster:    fmt.Sprintf("target-%d", i),
			Model:            "granite-3b",
			MaxBandwidthMbps: 1000,
		})
		if job != nil {
			jobIDs[i] = job.ID
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = orch.GetTransferStatus(ctx, jobIDs[i%n])
	}
}

func BenchmarkInitiateTransfer_10(b *testing.B)   { benchmarkInitiateTransfer(b, 10) }
func BenchmarkInitiateTransfer_50(b *testing.B)   { benchmarkInitiateTransfer(b, 50) }
func BenchmarkInitiateTransfer_100(b *testing.B)  { benchmarkInitiateTransfer(b, 100) }
func BenchmarkInitiateTransfer_250(b *testing.B)  { benchmarkInitiateTransfer(b, 250) }
func BenchmarkInitiateTransfer_500(b *testing.B)  { benchmarkInitiateTransfer(b, 500) }
func BenchmarkInitiateTransfer_1000(b *testing.B) { benchmarkInitiateTransfer(b, 1000) }

func BenchmarkGetTransferStatus_10(b *testing.B)   { benchmarkGetTransferStatus(b, 10) }
func BenchmarkGetTransferStatus_100(b *testing.B)  { benchmarkGetTransferStatus(b, 100) }
func BenchmarkGetTransferStatus_1000(b *testing.B) { benchmarkGetTransferStatus(b, 1000) }
