package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPLedgerClient connects to the ARE Immutable Ledger via its REST gateway.
// This is the production client for environments where the ARE gateway is available.
type HTTPLedgerClient struct {
	baseURL string
	http    *http.Client
}

// NewHTTPLedgerClient creates a ledger client that connects to the ARE REST gateway.
func NewHTTPLedgerClient(gatewayURL string) *HTTPLedgerClient {
	return &HTTPLedgerClient{
		baseURL: gatewayURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

type areWriteRequest struct {
	EntryType      string `json:"entry_type"`
	AgentID        string `json:"agent_id"`
	Content        string `json:"content"`
	ContentType    string `json:"content_type"`
	SourceID       string `json:"source_id"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	InputHash      string `json:"input_hash,omitempty"`
}

type areWriteResponse struct {
	EntryID       string `json:"entry_id"`
	EntryHash     string `json:"entry_hash"`
	ChainPosition string `json:"chain_position"`
	WrittenTs     int64  `json:"written_ts"`
}

type areVerifyResponse struct {
	ChainValid     bool   `json:"chain_valid"`
	EntriesChecked int64  `json:"entries_checked"`
	EntryType      string `json:"entry_type"`
}

type areReceiptResponse struct {
	EntryHash     string `json:"entry_hash"`
	EntryType     string `json:"entry_type"`
	ChainPosition string `json:"chain_position"`
	Timestamp     int64  `json:"timestamp"`
	InputHash     string `json:"input_hash"`
	EntryID       string `json:"entry_id"`
}

func (c *HTTPLedgerClient) RecordDecision(ctx context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	body, _ := json.Marshal(areWriteRequest{
		EntryType:      decision.Type,
		AgentID:        decision.AgentID,
		Content:        string(decision.Content),
		ContentType:    decision.ContentType,
		SourceID:       decision.SourceID,
		CorrelationID:  decision.CorrelationID,
		IdempotencyKey: decision.IdempotencyKey,
		InputHash:      decision.InputHash,
	})

	resp, err := c.doPost(ctx, "/api/entries", body)
	if err != nil {
		return nil, fmt.Errorf("ledger write failed: %w", err)
	}

	var result areWriteResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("ledger response parse failed: %w", err)
	}

	return &LedgerReceipt{
		EntryID:       result.EntryID,
		EntryHash:     result.EntryHash,
		ChainPosition: parseInt64(result.ChainPosition),
		Timestamp:     time.UnixMilli(result.WrittenTs),
	}, nil
}

func (c *HTTPLedgerClient) VerifyDecisionChain(ctx context.Context, decisionType string) (*ChainVerification, error) {
	resp, err := c.doGet(ctx, "/api/verify/"+decisionType)
	if err != nil {
		return nil, fmt.Errorf("chain verification failed: %w", err)
	}

	var result areVerifyResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("verify response parse failed: %w", err)
	}

	return &ChainVerification{
		Valid:          result.ChainValid,
		EntriesChecked: result.EntriesChecked,
		ChainType:      result.EntryType,
		VerifiedAt:     time.Now(),
	}, nil
}

func (c *HTTPLedgerClient) QueryDecisions(ctx context.Context, query DecisionQuery) ([]FleetDecision, error) {
	url := "/api/entries?entry_type=" + query.DecisionType
	if query.CorrelationID != "" {
		url += "&correlation_id=" + query.CorrelationID
	}

	resp, err := c.doGet(ctx, url)
	if err != nil {
		return nil, err
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, err
	}

	var decisions []FleetDecision
	for _, e := range entries {
		decisions = append(decisions, FleetDecision{
			Type:          fmt.Sprint(e["entry_type"]),
			AgentID:       fmt.Sprint(e["agent_id"]),
			SourceID:      fmt.Sprint(e["source_id"]),
			CorrelationID: fmt.Sprint(e["correlation_id"]),
		})
	}
	return decisions, nil
}

func (c *HTTPLedgerClient) IssueProofReceipt(ctx context.Context, decision FleetDecision) (*ProofReceipt, error) {
	body, _ := json.Marshal(areWriteRequest{
		EntryType:   decision.Type,
		AgentID:     decision.AgentID,
		Content:     string(decision.Content),
		ContentType: decision.ContentType,
		SourceID:    decision.SourceID,
		InputHash:   decision.InputHash,
	})

	resp, err := c.doPost(ctx, "/api/receipts", body)
	if err != nil {
		return nil, fmt.Errorf("receipt issue failed: %w", err)
	}

	var result areReceiptResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("receipt parse failed: %w", err)
	}

	return &ProofReceipt{
		EntryHash:     result.EntryHash,
		EntryType:     result.EntryType,
		ChainPosition: parseInt64(result.ChainPosition),
		Timestamp:     time.UnixMilli(result.Timestamp),
		InputHash:     result.InputHash,
	}, nil
}

func (c *HTTPLedgerClient) VerifyProof(ctx context.Context, entryHash, entryType string) (*ProofVerification, error) {
	resp, err := c.doGet(ctx, fmt.Sprintf("/api/receipts/verify?hash=%s&type=%s", entryHash, entryType))
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	return &ProofVerification{
		Valid:     result["valid"] == true,
		EntryType: entryType,
	}, nil
}

func (c *HTTPLedgerClient) doPost(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRequest(req)
}

func (c *HTTPLedgerClient) doGet(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

func (c *HTTPLedgerClient) doRequest(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ARE ledger returned %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func parseInt64(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
