package ledger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPLedgerClient_EscapesQueryParamsAndParsesBareArray(t *testing.T) {
	var receivedRawQuery string
	start := time.UnixMilli(1000)
	end := time.UnixMilli(2000)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRawQuery = r.URL.RawQuery
		if got := r.URL.Query().Get("from_ts"); got != "1000" {
			t.Errorf("from_ts = %q, want 1000", got)
		}
		if got := r.URL.Query().Get("to_ts"); got != "2000" {
			t.Errorf("to_ts = %q, want 2000", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"entry_id":"entry-1","entry_type":"fleet.scale","agent_id":"fleet-controller","content_raw":"{\"replicas\":2}","content_type":"application/json","source_id":"fleet-llm-d","correlation_id":"corr-1","entry_hash":"hash-1","chain_position":1,"written_ts":1234,"input_hash":"input-1"}]`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	decisions, err := client.QueryDecisions(context.Background(), DecisionQuery{
		DecisionType:  "type&injected=true",
		CorrelationID: "corr=bad&extra=yes",
		StartTime:     &start,
		EndTime:       &end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(receivedRawQuery, "&injected=true") || strings.Contains(receivedRawQuery, "&extra=yes") {
		t.Fatalf("query parameter injection succeeded: %s", receivedRawQuery)
	}
	if len(decisions) != 1 || string(decisions[0].Content) != `{"replicas":2}` || decisions[0].InputHash != "input-1" {
		t.Fatalf("unexpected decisions: %+v", decisions)
	}
}

func TestHTTPLedgerClient_UsesImmutableLedgerWriteContractAndBearerToken(t *testing.T) {
	var request ledgerWriteRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/entries" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer gateway-token" {
			t.Fatalf("missing gateway bearer token: %#v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"entry_id":"entry-1","entry_hash":"hash-1","chain_position":"7","written_ts":1234}`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClientWithToken(ts.URL, "gateway-token")
	receipt, err := client.RecordDecision(context.Background(), FleetDecision{
		Type: "fleet.scale", AgentID: "fleet-controller", SourceID: "fleet-llm-d",
		CorrelationID: "corr-1", IdempotencyKey: "idem-1", InputHash: "input-1",
		ContentType: "application/json", Content: []byte(`{"replicas":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EntryID != "entry-1" || receipt.ChainPosition != 7 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if request.Content != `{"replicas":2}` || request.IdempotencyKey != "idem-1" || request.InputHash != "input-1" {
		t.Fatalf("unexpected write request: %+v", request)
	}
}

func TestHTTPLedgerClient_UsesImmutableLedgerChainVerification(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/verify/fleet.scale" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"chain_valid":true,"entries_checked":3}`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	result, err := client.VerifyDecisionChain(context.Background(), "fleet.scale")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.EntriesChecked != 3 || result.ChainType != "fleet.scale" {
		t.Fatalf("unexpected verification: %+v", result)
	}
}

func TestHTTPLedgerClient_IssuesAndVerifiesOfficialProofReceipt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/receipts":
			var request ledgerWriteRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.InputHash != "cache-input-hash" {
				t.Fatalf("input hash was not committed: %+v", request)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"entry_id":"entry-1","entry_hash":"hash-1","entry_type":"fleet.kvcache.transferred","chain_position":4,"written_ts":1234,"input_hash":"cache-input-hash"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/receipts/verify":
			if r.URL.Query().Get("hash") != "hash-1" || r.URL.Query().Get("type") != "fleet.kvcache.transferred" {
				t.Fatalf("unexpected verify query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"valid":true,"entry_type":"fleet.kvcache.transferred","agent_id":"fleet-controller","source_id":"fleet-llm-d","correlation_id":"corr-1","content_type":"application/json","input_hash":"cache-input-hash","written_ts":1234,"chain_position":4}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/entries/by-hash":
			_, _ = w.Write([]byte(`{"entry_id":"entry-1","entry_hash":"hash-1","entry_type":"fleet.kvcache.transferred","agent_id":"fleet-controller","source_id":"fleet-llm-d","correlation_id":"corr-1","content_type":"application/json","content_raw":"{\"bytes\":42}","input_hash":"cache-input-hash","written_ts":1234,"chain_position":4}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	receipt, err := client.IssueProofReceipt(context.Background(), FleetDecision{
		Type: "fleet.kvcache.transferred", AgentID: "fleet-controller", SourceID: "fleet-llm-d",
		CorrelationID: "corr-1", InputHash: "cache-input-hash", Content: []byte(`{"bytes":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EntryID != "entry-1" || receipt.EntryHash != "hash-1" || receipt.ChainPosition != 4 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	verification, err := client.VerifyProof(context.Background(), receipt.EntryHash, receipt.EntryType)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid || verification.EntryID != "entry-1" || verification.CorrelationID != "corr-1" || verification.InputHash != "cache-input-hash" || verification.ChainPosition != 4 || string(verification.Content) != `{"bytes":42}` {
		t.Fatalf("unexpected proof verification: %+v", verification)
	}
}

func TestHTTPLedgerClient_RejectsBinaryContent(t *testing.T) {
	client := NewHTTPLedgerClient("http://ledger.invalid")
	_, err := client.RecordDecision(context.Background(), FleetDecision{
		Type: "fleet.binary", Content: []byte{0xff, 0xfe},
	})
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("expected explicit REST binary rejection, got %v", err)
	}
}
