package ledger

import (
	"context"
	"testing"
)

func TestNewLedgerClient(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestRecordDecision_NotImplemented(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	_, err := client.RecordDecision(context.Background(), FleetDecision{
		Type:    "fleet.placement.assigned",
		AgentID: "fleet-controller-1",
	})
	if err == nil {
		t.Fatal("expected error for not-implemented RecordDecision")
	}
}

func TestVerifyDecisionChain_NotImplemented(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	_, err := client.VerifyDecisionChain(context.Background(), "fleet.placement.assigned")
	if err == nil {
		t.Fatal("expected error for not-implemented VerifyDecisionChain")
	}
}

func TestQueryDecisions_NotImplemented(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	_, err := client.QueryDecisions(context.Background(), DecisionQuery{
		DecisionType: "fleet.placement.assigned",
	})
	if err == nil {
		t.Fatal("expected error for not-implemented QueryDecisions")
	}
}

func TestIssueProofReceipt_NotImplemented(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	_, err := client.IssueProofReceipt(context.Background(), FleetDecision{
		Type: "fleet.kvcache.transferred",
	})
	if err == nil {
		t.Fatal("expected error for not-implemented IssueProofReceipt")
	}
}

func TestVerifyProof_NotImplemented(t *testing.T) {
	client := NewLedgerClient("localhost:50051")
	_, err := client.VerifyProof(context.Background(), "abc123hash", "fleet.placement.assigned")
	if err == nil {
		t.Fatal("expected error for not-implemented VerifyProof")
	}
}
