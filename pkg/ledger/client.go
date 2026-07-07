package ledger

import (
	"context"
	"fmt"
)

// LedgerClient provides fleet-llm-d integration with the ARE Immutable Ledger.
type LedgerClient interface {
	// RecordDecision writes a fleet decision to the immutable ledger.
	RecordDecision(ctx context.Context, decision FleetDecision) (*LedgerReceipt, error)
	// VerifyDecisionChain verifies the integrity of a fleet decision chain.
	VerifyDecisionChain(ctx context.Context, decisionType string) (*ChainVerification, error)
	// QueryDecisions queries fleet decisions with filters.
	QueryDecisions(ctx context.Context, query DecisionQuery) ([]FleetDecision, error)
	// IssueProofReceipt writes and returns a compact proof receipt.
	IssueProofReceipt(ctx context.Context, decision FleetDecision) (*ProofReceipt, error)
	// VerifyProof validates a proof receipt.
	VerifyProof(ctx context.Context, entryHash, entryType string) (*ProofVerification, error)
}

// grpcLedgerClient implements LedgerClient via gRPC to the ARE ledger.
type grpcLedgerClient struct {
	endpoint string
}

// NewLedgerClient creates a new gRPC-backed LedgerClient.
func NewLedgerClient(endpoint string) LedgerClient {
	return &grpcLedgerClient{endpoint: endpoint}
}

func (c *grpcLedgerClient) RecordDecision(ctx context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	return nil, fmt.Errorf("not implemented: RecordDecision")
}

func (c *grpcLedgerClient) VerifyDecisionChain(ctx context.Context, decisionType string) (*ChainVerification, error) {
	return nil, fmt.Errorf("not implemented: VerifyDecisionChain")
}

func (c *grpcLedgerClient) QueryDecisions(ctx context.Context, query DecisionQuery) ([]FleetDecision, error) {
	return nil, fmt.Errorf("not implemented: QueryDecisions")
}

func (c *grpcLedgerClient) IssueProofReceipt(ctx context.Context, decision FleetDecision) (*ProofReceipt, error) {
	return nil, fmt.Errorf("not implemented: IssueProofReceipt")
}

func (c *grpcLedgerClient) VerifyProof(ctx context.Context, entryHash, entryType string) (*ProofVerification, error) {
	return nil, fmt.Errorf("not implemented: VerifyProof")
}
