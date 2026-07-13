package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
}

type areWriteResponse struct {
	EntryID       string `json:"entry_id"`
	EntryHash     string `json:"entry_hash"`
	ChainPosition string `json:"chain_position"`
	WrittenTs     int64  `json:"written_ts"`
}

type areVerifyChainResponse struct {
	ChainValid     bool   `json:"chain_valid"`
	EntriesChecked int64  `json:"entries_checked"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

type areVerifyEntryResponse struct {
	EntryID        string `json:"entry_id"`
	HashValid      bool   `json:"hash_valid"`
	ChainLinkValid bool   `json:"chain_link_valid"`
}

type areLedgerEntry struct {
	EntryID       string `json:"entry_id"`
	EntryType     string `json:"entry_type"`
	AgentID       string `json:"agent_id"`
	Content       string `json:"content"`
	ContentType   string `json:"content_type"`
	SourceID      string `json:"source_id"`
	CorrelationID string `json:"correlation_id"`
	EntryHash     string `json:"entry_hash"`
	ChainPosition int64  `json:"chain_position"`
	WrittenTS     int64  `json:"written_ts"`
}

type areQueryResponse struct {
	Entries       []areLedgerEntry `json:"entries"`
	TotalCount    int64            `json:"total_count"`
	NextPageToken string           `json:"next_page_token,omitempty"`
}

func (c *HTTPLedgerClient) RecordDecision(ctx context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	if decision.ContentType == "" {
		decision.ContentType = "application/octet-stream"
	}
	if decision.IdempotencyKey == "" {
		digest := sha256.Sum256(append([]byte(decision.Type+"\x00"+decision.CorrelationID+"\x00"), decision.Content...))
		decision.IdempotencyKey = fmt.Sprintf("fleet-%x", digest[:])
	}
	body, err := json.Marshal(areWriteRequest{
		EntryType:      decision.Type,
		AgentID:        decision.AgentID,
		Content:        base64.StdEncoding.EncodeToString(decision.Content),
		ContentType:    decision.ContentType,
		SourceID:       decision.SourceID,
		CorrelationID:  decision.CorrelationID,
		IdempotencyKey: decision.IdempotencyKey,
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling ledger request: %w", err)
	}

	headers := map[string]string{
		"Idempotency-Key": decision.IdempotencyKey,
		"X-Request-ID":    firstNonEmpty(decision.CorrelationID, decision.IdempotencyKey),
		"X-Are-Agent-ID":  decision.AgentID,
	}
	resp, err := c.doPost(ctx, "/v1/ledger/entries", body, headers)
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
	body, err := json.Marshal(map[string]string{"entry_type": decisionType})
	if err != nil {
		return nil, fmt.Errorf("marshal chain verification request: %w", err)
	}
	resp, err := c.doPost(ctx, "/v1/ledger/chains:verify", body, nil)
	if err != nil {
		return nil, fmt.Errorf("chain verification failed: %w", err)
	}

	var result areVerifyChainResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("verify response parse failed: %w", err)
	}

	return &ChainVerification{
		Valid:          result.ChainValid,
		EntriesChecked: result.EntriesChecked,
		ChainType:      decisionType,
		VerifiedAt:     time.Now(),
	}, nil
}

func (c *HTTPLedgerClient) QueryDecisions(ctx context.Context, query DecisionQuery) ([]FleetDecision, error) {
	params := url.Values{}
	params.Set("entry_type", query.DecisionType)
	if query.CorrelationID != "" {
		params.Set("correlation_id", query.CorrelationID)
	}
	if query.AgentID != "" {
		params.Set("agent_id", query.AgentID)
	}
	if query.SourceID != "" {
		params.Set("source_id", query.SourceID)
	}
	if query.StartTime != nil {
		params.Set("from_ts", fmt.Sprint(query.StartTime.UnixMilli()))
	}
	if query.EndTime != nil {
		params.Set("to_ts", fmt.Sprint(query.EndTime.UnixMilli()))
	}
	if query.Limit > 0 {
		params.Set("page_size", fmt.Sprint(query.Limit))
	}
	path := "/v1/ledger/entries?" + params.Encode()

	resp, err := c.doGet(ctx, path)
	if err != nil {
		return nil, err
	}

	var page areQueryResponse
	if err := json.Unmarshal(resp, &page); err != nil {
		return nil, err
	}

	decisions := make([]FleetDecision, 0, len(page.Entries))
	for _, entry := range page.Entries {
		content, decodeErr := base64.StdEncoding.DecodeString(entry.Content)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode ledger entry %q content: %w", entry.EntryID, decodeErr)
		}
		decisions = append(decisions, FleetDecision{
			Type:          entry.EntryType,
			AgentID:       entry.AgentID,
			SourceID:      entry.SourceID,
			CorrelationID: entry.CorrelationID,
			Content:       content,
			ContentType:   entry.ContentType,
		})
	}
	return decisions, nil
}

func (c *HTTPLedgerClient) IssueProofReceipt(ctx context.Context, decision FleetDecision) (*ProofReceipt, error) {
	receipt, err := c.RecordDecision(ctx, decision)
	if err != nil {
		return nil, fmt.Errorf("receipt issue failed: %w", err)
	}
	return &ProofReceipt{
		EntryHash:     receipt.EntryHash,
		EntryType:     decision.Type,
		ChainPosition: receipt.ChainPosition,
		Timestamp:     receipt.Timestamp,
		InputHash:     decision.InputHash,
	}, nil
}

func (c *HTTPLedgerClient) VerifyProof(ctx context.Context, entryHash, entryType string) (*ProofVerification, error) {
	params := url.Values{}
	params.Set("entry_type", entryType)
	resp, err := c.doGet(ctx, "/v1/ledger/entries?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var page areQueryResponse
	if err := json.Unmarshal(resp, &page); err != nil {
		return nil, err
	}
	for _, entry := range page.Entries {
		if entry.EntryHash != entryHash {
			continue
		}
		verified, verifyErr := c.doPost(ctx, "/v1/ledger/entries/"+url.PathEscape(entry.EntryID)+":verify", []byte(`{}`), nil)
		if verifyErr != nil {
			return nil, verifyErr
		}
		var result areVerifyEntryResponse
		if err := json.Unmarshal(verified, &result); err != nil {
			return nil, err
		}
		return &ProofVerification{
			Valid:     result.HashValid && result.ChainLinkValid,
			EntryID:   entry.EntryID,
			EntryType: entry.EntryType,
			AgentID:   entry.AgentID,
			SourceID:  entry.SourceID,
			WrittenAt: time.UnixMilli(entry.WrittenTS),
		}, nil
	}
	return &ProofVerification{Valid: false, EntryType: entryType}, nil
}

func (c *HTTPLedgerClient) doPost(ctx context.Context, path string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
	}
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

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
