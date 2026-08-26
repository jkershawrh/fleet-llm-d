package signals

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type staticStore []Sample

func (s staticStore) Samples(context.Context) ([]Sample, error) { return s, nil }

func TestPublisherRequiresPeerAndFiltersAtSource(t *testing.T) {
	cert := &x509.Certificate{Raw: []byte("peer")}
	now := time.Now().UTC()
	p := &Publisher{
		Site: "arena", Provider: "pool-a",
		Store: staticStore{
			{Name: "llm_d_epp_average_queue_size", Value: .5, CollectedAt: now},
			{Name: "llm_d_epp_request_count", Value: 4, CollectedAt: now},
			{Name: "unsafe_metric", Labels: map[string]string{"model_server_pod": "secret-pod"}, Value: 1, CollectedAt: now},
		},
		AllowedMetrics:          map[string]struct{}{"llm_d_epp_average_queue_size": {}, "llm_d_epp_request_count": {}, "unsafe_metric": {}},
		AllowedPeerFingerprints: map[string]struct{}{CertificateFingerprint(cert): {}},
	}
	unauthorized := httptest.NewRecorder()
	p.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("status = %d", unauthorized.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "grid_site=\"arena\"") || strings.Contains(body, "_count") || strings.Contains(body, "secret-pod") {
		t.Fatalf("unsafe output: %s", body)
	}
}

func TestClientEnforcesHTTPSSizeIdentityAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", now.Format(http.TimeFormat))
		_, _ = io.WriteString(w, "metric{grid_site=\"arena\",grid_provider=\"pool\"} 1 "+strings.TrimSpace(timeToMillis(now.Add(-time.Second)))+"\n")
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), MaxResponseBytes: 1024, MaxStaleness: 5 * time.Second}
	samples, err := c.Poll(context.Background(), server.URL, "arena")
	if err != nil || len(samples) != 1 {
		t.Fatalf("Poll = %v, %v", samples, err)
	}
	if _, err := c.Poll(context.Background(), strings.Replace(server.URL, "https://", "http://", 1), "arena"); err == nil {
		t.Fatal("plaintext endpoint accepted")
	}
	if _, err := c.Poll(context.Background(), server.URL, "oberon"); err == nil {
		t.Fatal("peer identity mismatch accepted")
	}
	c.MaxResponseBytes = 8
	if _, err := c.Poll(context.Background(), server.URL, "arena"); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func timeToMillis(value time.Time) string { return fmt.Sprintf("%d", value.UnixMilli()) }
