package ledger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPLedgerClient_EscapesQueryParams(t *testing.T) {
	var receivedRawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[],"total_count":0}`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	_, _ = client.QueryDecisions(context.Background(), DecisionQuery{
		DecisionType:  "type&injected=true",
		CorrelationID: "corr=bad&extra=yes",
	})

	if strings.Contains(receivedRawQuery, "&injected=true") {
		t.Error("query parameter injection succeeded - DecisionType was not escaped")
	}
	if strings.Contains(receivedRawQuery, "&extra=yes") {
		t.Error("query parameter injection succeeded - CorrelationID was not escaped")
	}
}

func TestHTTPLedgerClient_UsesCanonicalWriteContract(t *testing.T) {
	var request areWriteRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/ledger/entries" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") != "idem-1" || r.Header.Get("X-Are-Agent-ID") != "fleet-controller" {
			t.Fatalf("missing ARE headers: %#v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"entry_id":"entry-1","entry_hash":"hash-1","chain_position":"7","written_ts":1234}`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	receipt, err := client.RecordDecision(context.Background(), FleetDecision{
		Type: "fleet.scale", AgentID: "fleet-controller", SourceID: "fleet-llm-d",
		CorrelationID: "corr-1", IdempotencyKey: "idem-1", ContentType: "application/json", Content: []byte(`{"replicas":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EntryID != "entry-1" || receipt.ChainPosition != 7 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	wantContent := base64.StdEncoding.EncodeToString([]byte(`{"replicas":2}`))
	if request.Content != wantContent {
		t.Fatalf("content = %q, want base64 %q", request.Content, wantContent)
	}
}

func TestHTTPLedgerClient_UsesCanonicalChainVerification(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ledger/chains:verify" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"entry_type":"fleet.scale"`) {
			t.Fatalf("unexpected request body %s", body)
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

func TestHTTPLedgerClient_VerifiesProofThroughOfficialEntryAPI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/ledger/entries":
			if r.URL.Query().Get("entry_type") != "fleet.scale" {
				t.Fatalf("entry_type was not encoded")
			}
			_, _ = w.Write([]byte(`{"entries":[{"entry_id":"entry-1","entry_type":"fleet.scale","agent_id":"fleet-controller","content":"e30=","content_type":"application/json","source_id":"fleet-llm-d","entry_hash":"hash-1","chain_position":1,"written_ts":1234}],"total_count":1}`))
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/ledger/entries/entry-1:verify":
			_, _ = w.Write([]byte(`{"entry_id":"entry-1","hash_valid":true,"chain_link_valid":true}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	result, err := client.VerifyProof(context.Background(), "hash-1", "fleet.scale")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.EntryID != "entry-1" {
		t.Fatalf("unexpected proof result: %+v", result)
	}
}
