package signals

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPrometheusStorePublishesOnlyAggregatedAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`
llm_d_epp_per_endpoint_queue_size{pod="one",instance="10.0.0.1"} 2
llm_d_epp_per_endpoint_queue_size{pod="two",instance="10.0.0.2"} 4
llm_d_epp_per_endpoint_kv_cache_utilization{pod="one"} 0.2
llm_d_epp_per_endpoint_kv_cache_utilization{pod="two"} 0.6
llm_d_epp_ready_endpoints{name="pool"} 2
unsafe_secret_metric{token="secret"} 99
`))
	}))
	defer server.Close()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store := &PrometheusStore{Endpoint: server.URL, Now: func() time.Time { return now }}
	samples, err := store.Samples(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Sample{}
	for _, sample := range samples {
		got[sample.Name] = sample
		if len(sample.Labels) != 0 {
			t.Fatalf("source labels escaped: %#v", sample.Labels)
		}
	}
	if got["llm_d_epp_average_queue_size"].Value != 3 || got["llm_d_epp_average_kv_cache_utilization"].Value != 0.4 || got["llm_d_epp_ready_endpoints"].Value != 2 {
		t.Fatalf("samples = %#v", got)
	}
	if _, ok := got["unsafe_secret_metric"]; ok {
		t.Fatal("non-allowlisted metric escaped")
	}
}

func TestPrometheusStorePrefersCanonicalPoolMetric(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("llm_d_epp_per_endpoint_queue_size 100\nllm_d_epp_average_queue_size 7\n"))
	}))
	defer server.Close()
	samples, err := (&PrometheusStore{Endpoint: server.URL}).Samples(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].Value != 7 {
		t.Fatalf("samples = %#v", samples)
	}
}

func TestPrometheusStoreRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("llm_d_epp_ready_endpoints 2\n"))
	}))
	defer server.Close()
	_, err := (&PrometheusStore{Endpoint: server.URL, MaxResponseBytes: 8}).Samples(context.Background())
	if err == nil {
		t.Fatal("oversized response accepted")
	}
}
