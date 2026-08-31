package ledger

import "time"

// FleetDecision represents a fleet orchestration decision to be recorded in
// the standalone immutable ledger.
type FleetDecision struct {
	Type           string
	AgentID        string
	SourceID       string
	CorrelationID  string
	Content        []byte
	ContentType    string
	IdempotencyKey string
	InputHash      string
	Metadata       map[string]string
}

// LedgerReceipt is returned after recording a decision.
type LedgerReceipt struct {
	EntryID       string
	EntryHash     string
	ChainPosition int64
	Timestamp     time.Time
}

// ProofReceipt is a compact, portable proof that travels with requests.
type ProofReceipt struct {
	EntryID       string
	EntryHash     string
	EntryType     string
	ChainPosition int64
	Timestamp     time.Time
	InputHash     string
}

// ChainVerification is the result of verifying a decision chain.
type ChainVerification struct {
	Valid          bool
	EntriesChecked int64
	ChainType      string
	VerifiedAt     time.Time
}

// ProofVerification is the result of verifying a proof receipt.
type ProofVerification struct {
	Valid         bool
	EntryID       string
	EntryType     string
	AgentID       string
	SourceID      string
	CorrelationID string
	InputHash     string
	// Content is the exact entry payload committed by EntryHash. Callers that
	// use a proof as operation evidence must validate this payload rather than
	// relying on reusable metadata such as a correlation or input digest alone.
	Content       []byte
	ChainPosition int64
	FailureReason string
	WrittenAt     time.Time
}

// DecisionQuery filters for querying decisions.
type DecisionQuery struct {
	DecisionType  string
	AgentID       string
	SourceID      string
	CorrelationID string
	StartTime     *time.Time
	EndTime       *time.Time
	Limit         int32
}
