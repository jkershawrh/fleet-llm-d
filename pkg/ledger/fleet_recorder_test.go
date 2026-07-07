package ledger

import (
	"context"
	"testing"
)

func TestRecordPlacement(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	recorder := NewFleetRecorder(client, "fleet-controller-1", "placement-engine")

	_, err := recorder.RecordPlacement(context.Background(), "llama-3-70b", "us-east-1", 3, "A100", "high demand")
	if err == nil {
		t.Fatal("expected not-implemented error from RecordPlacement")
	}
}

func TestRecordRoutingChange(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	recorder := NewFleetRecorder(client, "fleet-controller-1", "routing-engine")

	_, err := recorder.RecordRoutingChange(context.Background(), "llama-3-70b", "us-east-1", "us-west-2", 0.3, "latency optimization")
	if err == nil {
		t.Fatal("expected not-implemented error from RecordRoutingChange")
	}
}

func TestRecordScalingEvent(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	recorder := NewFleetRecorder(client, "fleet-controller-1", "autoscaler")

	_, err := recorder.RecordScalingEvent(context.Background(), "us-east-1", "gpu-pool-a100", 3, 5, "queue depth exceeded threshold")
	if err == nil {
		t.Fatal("expected not-implemented error from RecordScalingEvent")
	}
}

func TestRecordTenantUsage(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	recorder := NewFleetRecorder(client, "fleet-controller-1", "metering")

	_, err := recorder.RecordTenantUsage(context.Background(), "acme-corp", "llama-3-70b", "us-east-1", 150000, "0.0045")
	if err == nil {
		t.Fatal("expected not-implemented error from RecordTenantUsage")
	}
}

func TestVerifyAllChains(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	recorder := NewFleetRecorder(client, "fleet-controller-1", "verifier")

	_, err := recorder.VerifyAllChains(context.Background())
	if err == nil {
		t.Fatal("expected not-implemented error from VerifyAllChains")
	}
}

func TestRecordKVCacheTransfer_ReturnsProofReceipt(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	recorder := NewFleetRecorder(client, "fleet-controller-1", "kv-transfer")

	_, err := recorder.RecordKVCacheTransfer(context.Background(), "us-east-1", "us-west-2", "llama-3-70b", 1073741824, "abcdef1234567890")
	if err == nil {
		t.Fatal("expected not-implemented error from RecordKVCacheTransfer")
	}
}
