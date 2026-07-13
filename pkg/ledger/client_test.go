package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestNewLedgerClient_DefaultsToMemoryClient(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	receipt, err := client.RecordDecision(context.Background(), FleetDecision{
		Type:    "fleet.placement.assigned",
		AgentID: "fleet-controller-1",
	})
	if err != nil {
		t.Fatalf("RecordDecision() unexpected error: %v", err)
	}
	if receipt.EntryID == "" {
		t.Fatal("expected receipt entry ID")
	}
}

func TestInMemoryLedgerClient_VerifyProofRejectsUnknown(t *testing.T) {
	lc := NewInMemoryLedgerClient()
	result, err := lc.VerifyProof(context.Background(), "nonexistent-hash", "fleet.placement.assigned")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Error("VerifyProof should return Valid=false for unknown hash")
	}
}

func TestInMemoryLedgerClient_VerifyProofAcceptsKnown(t *testing.T) {
	lc := NewInMemoryLedgerClient()

	// Record a decision with a known input hash
	decision := FleetDecision{
		Type:      "fleet.placement.assigned",
		InputHash: "known-hash-123",
		Content:   []byte(`{"model":"test"}`),
	}
	_, err := lc.IssueProofReceipt(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the proof for the known hash
	result, err := lc.VerifyProof(context.Background(), "known-hash-123", "fleet.placement.assigned")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Error("VerifyProof should return Valid=true for a hash that was recorded")
	}
}

func TestInMemoryLedgerClient_VerifyProofWrongType(t *testing.T) {
	lc := NewInMemoryLedgerClient()

	decision := FleetDecision{
		Type:      "fleet.placement.assigned",
		InputHash: "hash-456",
		Content:   []byte(`{"model":"test"}`),
	}
	_, err := lc.IssueProofReceipt(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong type should not match
	result, err := lc.VerifyProof(context.Background(), "hash-456", "fleet.routing.shifted")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Error("VerifyProof should return Valid=false when type doesn't match")
	}
}

func TestNewLedgerClientWithConfig_Modes(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		expectErr bool
	}{
		{name: "disabled", cfg: Config{Mode: ModeDisabled}},
		{name: "memory", cfg: Config{Mode: ModeMemory}},
		{name: "http", cfg: Config{Mode: ModeHTTP, Endpoint: "http://ledger.example"}},
		{name: "grpc_not_implemented", cfg: Config{Mode: ModeGRPC, Endpoint: "ledger.example:50051"}, expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewLedgerClientWithConfig(tt.cfg)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error for unimplemented grpc mode")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewLedgerClientWithConfig() unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

func TestDisabledLedgerNeverFabricatesEvidence(t *testing.T) {
	client, err := NewLedgerClientWithConfig(Config{Mode: ModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := client.RecordDecision(context.Background(), FleetDecision{Type: "fleet.scale"}); receipt != nil || !errors.Is(err, ErrLedgerDisabled) {
		t.Fatalf("RecordDecision() = (%#v, %v), want nil ErrLedgerDisabled", receipt, err)
	}
	if verification, err := client.VerifyDecisionChain(context.Background(), "fleet.scale"); verification == nil || verification.Valid || !errors.Is(err, ErrLedgerDisabled) {
		t.Fatalf("VerifyDecisionChain() = (%#v, %v), want invalid ErrLedgerDisabled", verification, err)
	}
	if proof, err := client.IssueProofReceipt(context.Background(), FleetDecision{Type: "fleet.scale"}); proof != nil || !errors.Is(err, ErrLedgerDisabled) {
		t.Fatalf("IssueProofReceipt() = (%#v, %v), want nil ErrLedgerDisabled", proof, err)
	}
	if verification, err := client.VerifyProof(context.Background(), "hash", "fleet.scale"); verification == nil || verification.Valid || !errors.Is(err, ErrLedgerDisabled) {
		t.Fatalf("VerifyProof() = (%#v, %v), want invalid ErrLedgerDisabled", verification, err)
	}
}
