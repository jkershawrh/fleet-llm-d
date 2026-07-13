package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollectLedgerAuthenticatesCompatibilityGatewayRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ledger-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/summary":
			_, _ = w.Write([]byte(`{"total_entries":4,"sources":{"gcl":1,"fleet-llm-d":3}}`))
		case "/api/verify":
			_, _ = w.Write([]byte(`{"all_valid":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	collector := &PlatformCollector{LedgerURL: server.URL, LedgerToken: "ledger-token"}
	got := collector.collectLedger(context.Background(), server.Client())
	if got == nil || got.TotalEntries != 4 || got.GCLEntries != 1 || !got.ChainsValid {
		t.Fatalf("unexpected ledger metrics: %+v", got)
	}
}
