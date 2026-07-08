package ledger

import (
	"context"
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
		w.WriteHeader(200)
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	_, _ = client.QueryDecisions(context.Background(), DecisionQuery{
		DecisionType:  "type&injected=true",
		CorrelationID: "corr=bad&extra=yes",
	})

	if strings.Contains(receivedRawQuery, "&injected=true") {
		t.Error("query parameter injection succeeded — DecisionType was not escaped")
	}
	if strings.Contains(receivedRawQuery, "&extra=yes") {
		t.Error("query parameter injection succeeded — CorrelationID was not escaped")
	}
}

func TestHTTPLedgerClient_EscapesPathSegments(t *testing.T) {
	var receivedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"chain_valid":true,"entries_checked":0}`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	_, _ = client.VerifyDecisionChain(context.Background(), "../../admin")

	if strings.Contains(receivedPath, "/../") || strings.Contains(receivedPath, "/admin") {
		t.Errorf("path traversal succeeded — decisionType was not escaped: %s", receivedPath)
	}
}

func TestHTTPLedgerClient_VerifyProofEscapesParams(t *testing.T) {
	var receivedRawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"valid":true}`))
	}))
	defer ts.Close()

	client := NewHTTPLedgerClient(ts.URL)
	_, _ = client.VerifyProof(context.Background(), "hash&inject=1", "type&bad=2")

	if strings.Contains(receivedRawQuery, "&inject=1") {
		t.Error("query injection via entryHash — not escaped")
	}
	if strings.Contains(receivedRawQuery, "&bad=2") {
		t.Error("query injection via entryType — not escaped")
	}
}
