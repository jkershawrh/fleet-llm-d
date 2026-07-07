package ledger

import (
	"context"
	"fmt"
	"time"
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

// InMemoryLedgerClient stores entries in memory for testing.
type InMemoryLedgerClient struct {
	entries []FleetDecision
	counter int64
}

// NewInMemoryLedgerClient creates a ledger client backed by in-memory storage.
func NewInMemoryLedgerClient() *InMemoryLedgerClient {
	return &InMemoryLedgerClient{}
}

func (c *InMemoryLedgerClient) RecordDecision(_ context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	c.counter++
	c.entries = append(c.entries, decision)
	return &LedgerReceipt{
		EntryID:       fmt.Sprintf("entry-%d", c.counter),
		EntryHash:     fmt.Sprintf("hash-%d", c.counter),
		ChainPosition: c.counter,
		Timestamp:     time.Now(),
	}, nil
}

func (c *InMemoryLedgerClient) VerifyDecisionChain(_ context.Context, decisionType string) (*ChainVerification, error) {
	count := int64(0)
	for _, e := range c.entries {
		if e.Type == decisionType {
			count++
		}
	}
	return &ChainVerification{
		Valid:          true,
		EntriesChecked: count,
		ChainType:      decisionType,
		VerifiedAt:     time.Now(),
	}, nil
}

func (c *InMemoryLedgerClient) QueryDecisions(_ context.Context, query DecisionQuery) ([]FleetDecision, error) {
	var result []FleetDecision
	for _, e := range c.entries {
		if query.DecisionType != "" && e.Type != query.DecisionType {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

func (c *InMemoryLedgerClient) IssueProofReceipt(_ context.Context, decision FleetDecision) (*ProofReceipt, error) {
	c.counter++
	c.entries = append(c.entries, decision)
	return &ProofReceipt{
		EntryHash:     fmt.Sprintf("proof-hash-%d", c.counter),
		EntryType:     decision.Type,
		ChainPosition: c.counter,
		Timestamp:     time.Now(),
		InputHash:     decision.InputHash,
	}, nil
}

func (c *InMemoryLedgerClient) VerifyProof(_ context.Context, entryHash, entryType string) (*ProofVerification, error) {
	return &ProofVerification{
		Valid:     true,
		EntryType: entryType,
	}, nil
}

// Entries returns all recorded entries (for test assertions).
func (c *InMemoryLedgerClient) Entries() []FleetDecision {
	return c.entries
}
