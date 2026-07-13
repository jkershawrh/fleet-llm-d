package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrLedgerDisabled is returned whenever a caller asks for durable evidence
// while no ledger backend is configured. Disabled mode must never fabricate a
// receipt or report a chain as valid.
var ErrLedgerDisabled = errors.New("immutable ledger is disabled")

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

// Mode selects the ledger transport/backend.
type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeMemory   Mode = "memory"
	ModeHTTP     Mode = "http"
	ModeGRPC     Mode = "grpc"
)

// Config controls ledger client construction.
type Config struct {
	Mode     Mode
	Endpoint string
}

// NewLedgerClient creates a development-safe ledger client. Use
// NewLedgerClientWithConfig for explicit production transport selection.
func NewLedgerClient(endpoint string) LedgerClient {
	client, err := NewLedgerClientWithConfig(Config{Mode: ModeMemory, Endpoint: endpoint})
	if err != nil {
		return NewInMemoryLedgerClient()
	}
	return client
}

// NewLedgerClientWithConfig creates a ledger client for the selected mode.
func NewLedgerClientWithConfig(cfg Config) (LedgerClient, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeMemory
	}

	switch mode {
	case ModeDisabled:
		return disabledLedgerClient{}, nil
	case ModeMemory:
		return NewInMemoryLedgerClient(), nil
	case ModeHTTP:
		if strings.TrimSpace(cfg.Endpoint) == "" {
			return nil, fmt.Errorf("ledger endpoint is required for http mode")
		}
		return NewHTTPLedgerClient(strings.TrimRight(cfg.Endpoint, "/")), nil
	case ModeGRPC:
		if strings.TrimSpace(cfg.Endpoint) == "" {
			return nil, fmt.Errorf("ledger endpoint is required for grpc mode")
		}
		return nil, fmt.Errorf("grpc ledger transport is not yet implemented (endpoint: %s); use --ledger-mode=http with the ARE REST gateway instead", cfg.Endpoint)
	default:
		return nil, fmt.Errorf("unsupported ledger mode %q", mode)
	}
}

type disabledLedgerClient struct{}

func (disabledLedgerClient) RecordDecision(_ context.Context, _ FleetDecision) (*LedgerReceipt, error) {
	return nil, ErrLedgerDisabled
}

func (disabledLedgerClient) VerifyDecisionChain(_ context.Context, decisionType string) (*ChainVerification, error) {
	return &ChainVerification{Valid: false, ChainType: decisionType, VerifiedAt: time.Now()}, ErrLedgerDisabled
}

func (disabledLedgerClient) QueryDecisions(_ context.Context, _ DecisionQuery) ([]FleetDecision, error) {
	return nil, ErrLedgerDisabled
}

func (disabledLedgerClient) IssueProofReceipt(_ context.Context, decision FleetDecision) (*ProofReceipt, error) {
	return nil, ErrLedgerDisabled
}

func (disabledLedgerClient) VerifyProof(_ context.Context, _ string, entryType string) (*ProofVerification, error) {
	return &ProofVerification{Valid: false, EntryType: entryType}, ErrLedgerDisabled
}

// InMemoryLedgerClient stores entries in memory for testing.
type InMemoryLedgerClient struct {
	mu      sync.Mutex
	entries []FleetDecision
	counter int64
}

// NewInMemoryLedgerClient creates a ledger client backed by in-memory storage.
func NewInMemoryLedgerClient() *InMemoryLedgerClient {
	return &InMemoryLedgerClient{}
}

func (c *InMemoryLedgerClient) RecordDecision(_ context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.InputHash == entryHash && e.Type == entryType {
			return &ProofVerification{Valid: true, EntryType: entryType}, nil
		}
	}
	return &ProofVerification{Valid: false, EntryType: entryType}, nil
}

// Entries returns all recorded entries (for test assertions).
func (c *InMemoryLedgerClient) Entries() []FleetDecision {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]FleetDecision, len(c.entries))
	copy(cp, c.entries)
	return cp
}
