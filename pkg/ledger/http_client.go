package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// HTTPLedgerClient connects to the optional REST gateway shipped by
// jkershawrh/are-immutable-ledger. The ledger-owned gRPC service is the
// canonical production contract; this adapter exists for compatibility and
// development deployments that intentionally expose the REST gateway.
type HTTPLedgerClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewHTTPLedgerClient creates an unauthenticated compatibility client.
func NewHTTPLedgerClient(gatewayURL string) *HTTPLedgerClient {
	return NewHTTPLedgerClientWithToken(gatewayURL, "")
}

// NewHTTPLedgerClientWithToken creates a REST gateway client that presents the
// gateway's optional bearer token.
func NewHTTPLedgerClientWithToken(gatewayURL, token string) *HTTPLedgerClient {
	return &HTTPLedgerClient{
		baseURL: strings.TrimRight(gatewayURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

type ledgerWriteRequest struct {
	EntryType      string `json:"entry_type"`
	AgentID        string `json:"agent_id"`
	Content        string `json:"content"`
	ContentType    string `json:"content_type"`
	SourceID       string `json:"source_id"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	InputHash      string `json:"input_hash,omitempty"`
}

// ledgerInt64 accepts both protobuf string-backed values returned by
// WriteEntry and numeric values returned by IssueReceipt.
type ledgerInt64 int64

func (v *ledgerInt64) UnmarshalJSON(data []byte) error {
	var n int64
	if len(data) > 0 && data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("parse ledger integer %q: %w", raw, err)
		}
		n = parsed
	} else if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = ledgerInt64(n)
	return nil
}

type ledgerWriteResponse struct {
	EntryID       string      `json:"entry_id"`
	EntryHash     string      `json:"entry_hash"`
	ChainPosition ledgerInt64 `json:"chain_position"`
	WrittenTS     int64       `json:"written_ts"`
}

type ledgerReceiptResponse struct {
	EntryID       string      `json:"entry_id"`
	EntryHash     string      `json:"entry_hash"`
	EntryType     string      `json:"entry_type"`
	ChainPosition ledgerInt64 `json:"chain_position"`
	WrittenTS     int64       `json:"written_ts"`
	InputHash     string      `json:"input_hash"`
}

type ledgerVerifyChainResponse struct {
	ChainValid     bool   `json:"chain_valid"`
	EntriesChecked int64  `json:"entries_checked"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

type ledgerProofVerificationResponse struct {
	Valid         bool        `json:"valid"`
	EntryType     string      `json:"entry_type"`
	AgentID       string      `json:"agent_id"`
	SourceID      string      `json:"source_id"`
	CorrelationID string      `json:"correlation_id"`
	ContentType   string      `json:"content_type"`
	InputHash     string      `json:"input_hash"`
	WrittenTS     int64       `json:"written_ts"`
	ChainPosition ledgerInt64 `json:"chain_position"`
	FailureReason string      `json:"failure_reason,omitempty"`
}

type ledgerEntry struct {
	EntryID       string      `json:"entry_id"`
	EntryType     string      `json:"entry_type"`
	AgentID       string      `json:"agent_id"`
	ContentRaw    string      `json:"content_raw"`
	ContentType   string      `json:"content_type"`
	SourceID      string      `json:"source_id"`
	CorrelationID string      `json:"correlation_id"`
	EntryHash     string      `json:"entry_hash"`
	ChainPosition ledgerInt64 `json:"chain_position"`
	WrittenTS     int64       `json:"written_ts"`
	InputHash     string      `json:"input_hash"`
}

func normalizeDecision(decision FleetDecision) (FleetDecision, error) {
	if decision.ContentType == "" {
		decision.ContentType = "application/json"
	}
	if !utf8.Valid(decision.Content) {
		return FleetDecision{}, fmt.Errorf("immutable-ledger REST gateway accepts UTF-8 content only; use the canonical gRPC transport for arbitrary bytes")
	}
	if decision.IdempotencyKey == "" {
		digest := sha256.Sum256(append([]byte(decision.Type+"\x00"+decision.CorrelationID+"\x00"), decision.Content...))
		decision.IdempotencyKey = fmt.Sprintf("fleet-%x", digest[:])
	}
	if decision.InputHash == "" {
		digest := sha256.Sum256(decision.Content)
		decision.InputHash = fmt.Sprintf("%x", digest[:])
	}
	return decision, nil
}

func marshalLedgerWrite(decision FleetDecision) ([]byte, FleetDecision, error) {
	normalized, err := normalizeDecision(decision)
	if err != nil {
		return nil, FleetDecision{}, err
	}
	body, err := json.Marshal(ledgerWriteRequest{
		EntryType:      normalized.Type,
		AgentID:        normalized.AgentID,
		Content:        string(normalized.Content),
		ContentType:    normalized.ContentType,
		SourceID:       normalized.SourceID,
		CorrelationID:  normalized.CorrelationID,
		IdempotencyKey: normalized.IdempotencyKey,
		InputHash:      normalized.InputHash,
	})
	if err != nil {
		return nil, FleetDecision{}, fmt.Errorf("marshalling immutable-ledger request: %w", err)
	}
	return body, normalized, nil
}

func (c *HTTPLedgerClient) RecordDecision(ctx context.Context, decision FleetDecision) (*LedgerReceipt, error) {
	body, _, err := marshalLedgerWrite(decision)
	if err != nil {
		return nil, err
	}
	resp, err := c.doPost(ctx, "/api/entries", body)
	if err != nil {
		return nil, fmt.Errorf("immutable-ledger write failed: %w", err)
	}

	var result ledgerWriteResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("immutable-ledger response parse failed: %w", err)
	}
	return &LedgerReceipt{
		EntryID:       result.EntryID,
		EntryHash:     result.EntryHash,
		ChainPosition: int64(result.ChainPosition),
		Timestamp:     time.UnixMilli(result.WrittenTS),
	}, nil
}

func (c *HTTPLedgerClient) VerifyDecisionChain(ctx context.Context, decisionType string) (*ChainVerification, error) {
	resp, err := c.doGet(ctx, "/api/verify/"+url.PathEscape(decisionType))
	if err != nil {
		return nil, fmt.Errorf("immutable-ledger chain verification failed: %w", err)
	}
	var result ledgerVerifyChainResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("immutable-ledger verify response parse failed: %w", err)
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
	if query.DecisionType != "" {
		params.Set("entry_type", query.DecisionType)
	}
	if query.CorrelationID != "" {
		params.Set("correlation_id", query.CorrelationID)
	}
	if query.AgentID != "" {
		params.Set("agent_id", query.AgentID)
	}
	if query.SourceID != "" {
		params.Set("source_id", query.SourceID)
	}
	if query.StartTime != nil && !query.StartTime.IsZero() {
		params.Set("from_ts", strconv.FormatInt(query.StartTime.UnixMilli(), 10))
	}
	if query.EndTime != nil && !query.EndTime.IsZero() {
		params.Set("to_ts", strconv.FormatInt(query.EndTime.UnixMilli(), 10))
	}

	path := "/api/entries"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.doGet(ctx, path)
	if err != nil {
		return nil, err
	}

	var entries []ledgerEntry
	if err := json.Unmarshal(resp, &entries); err != nil {
		return nil, fmt.Errorf("immutable-ledger query response parse failed: %w", err)
	}
	decisions := make([]FleetDecision, 0, len(entries))
	for _, entry := range entries {
		decisions = append(decisions, FleetDecision{
			Type:          entry.EntryType,
			AgentID:       entry.AgentID,
			SourceID:      entry.SourceID,
			CorrelationID: entry.CorrelationID,
			Content:       []byte(entry.ContentRaw),
			ContentType:   entry.ContentType,
			InputHash:     entry.InputHash,
		})
	}
	if query.Limit > 0 && len(decisions) > int(query.Limit) {
		decisions = decisions[:query.Limit]
	}
	return decisions, nil
}

func (c *HTTPLedgerClient) IssueProofReceipt(ctx context.Context, decision FleetDecision) (*ProofReceipt, error) {
	body, normalized, err := marshalLedgerWrite(decision)
	if err != nil {
		return nil, err
	}
	resp, err := c.doPost(ctx, "/api/receipts", body)
	if err != nil {
		return nil, fmt.Errorf("immutable-ledger receipt issue failed: %w", err)
	}
	var result ledgerReceiptResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("immutable-ledger receipt response parse failed: %w", err)
	}
	return &ProofReceipt{
		EntryID:       result.EntryID,
		EntryHash:     result.EntryHash,
		EntryType:     result.EntryType,
		ChainPosition: int64(result.ChainPosition),
		Timestamp:     time.UnixMilli(result.WrittenTS),
		InputHash:     firstNonEmpty(result.InputHash, normalized.InputHash),
	}, nil
}

func (c *HTTPLedgerClient) VerifyProof(ctx context.Context, entryHash, entryType string) (*ProofVerification, error) {
	params := url.Values{}
	params.Set("hash", entryHash)
	params.Set("type", entryType)
	resp, err := c.doGet(ctx, "/api/receipts/verify?"+params.Encode())
	if err != nil {
		return nil, fmt.Errorf("immutable-ledger proof verification failed: %w", err)
	}
	var result ledgerProofVerificationResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("immutable-ledger proof verification response parse failed: %w", err)
	}
	entryID := ""
	var content []byte
	if result.Valid {
		entryParams := url.Values{}
		entryParams.Set("hash", entryHash)
		entryParams.Set("type", entryType)
		entryResponse, entryErr := c.doGet(ctx, "/api/entries/by-hash?"+entryParams.Encode())
		if entryErr != nil {
			return nil, fmt.Errorf("immutable-ledger verified proof lookup failed: %w", entryErr)
		}
		var entry ledgerEntry
		if err := json.Unmarshal(entryResponse, &entry); err != nil {
			return nil, fmt.Errorf("immutable-ledger verified entry response parse failed: %w", err)
		}
		if entry.EntryHash != entryHash || entry.EntryType != entryType {
			return nil, fmt.Errorf("immutable-ledger verified entry identity mismatch")
		}
		entryID = entry.EntryID
		content = []byte(entry.ContentRaw)
	}
	return &ProofVerification{
		Valid:         result.Valid,
		EntryID:       entryID,
		EntryType:     result.EntryType,
		AgentID:       result.AgentID,
		SourceID:      result.SourceID,
		CorrelationID: result.CorrelationID,
		InputHash:     result.InputHash,
		Content:       content,
		ChainPosition: int64(result.ChainPosition),
		FailureReason: result.FailureReason,
		WrittenAt:     time.UnixMilli(result.WrittenTS),
	}, nil
}

func (c *HTTPLedgerClient) doPost(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRequest(req)
}

func (c *HTTPLedgerClient) doGet(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

func (c *HTTPLedgerClient) doRequest(req *http.Request) ([]byte, error) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("immutable-ledger gateway returned %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
