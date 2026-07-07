package ledger

import (
	"context"
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
