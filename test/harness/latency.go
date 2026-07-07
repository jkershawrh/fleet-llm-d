package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RunLatency performs 1000 sequential requests per endpoint category and
// computes p50/p95/p99/min/max/mean latency statistics.
func RunLatency(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "latency", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	iterations := 1000

	type category struct {
		Name     string
		Method   string
		Path     string
		Auth     bool
		HasBody  bool
	}

	categories := []category{
		{"health", "GET", "/healthz", false, false},
		{"auth-reads", "GET", "/api/v1/clusters", true, false},
		{"auth-writes", "POST", "/api/v1/clusters", true, true},
		{"metrics", "GET", "/api/v1/metrics/fleet", true, false},
	}

	for _, cat := range categories {
		if cat.Auth && cfg.Token == "" {
			skip(&sr, "latency:"+cat.Name, "no token provided")
			continue
		}

		var latencies []time.Duration
		var errors int

		for i := 0; i < iterations; i++ {
			var resp *http.Response
			var lat time.Duration
			var err error

			if !cat.Auth {
				resp, lat, err = doRequest(cat.Method, cfg.BaseURL+cat.Path, nil, nil)
			} else if cat.HasBody {
				body := clusterPayload(fmt.Sprintf("lat-%d", i))
				resp, lat, err = authPost(cfg.BaseURL, cat.Path, cfg.Token,
					strings.NewReader(body))
			} else {
				resp, lat, err = authGet(cfg.BaseURL, cat.Path, cfg.Token)
			}

			if err != nil {
				errors++
				continue
			}
			drainBody(resp)
			latencies = append(latencies, lat)
		}

		stats := computeLatencyStats(latencies)

		detail := fmt.Sprintf("n=%d errors=%d p50=%.1fms p95=%.1fms p99=%.1fms min=%.1fms max=%.1fms mean=%.1fms",
			len(latencies), errors,
			stats.P50, stats.P95, stats.P99,
			stats.Min, stats.Max, stats.Mean)

		check(&sr, "latency:"+cat.Name, len(latencies) > 0, detail, int64(stats.P50))

		sr.Extra[cat.Name+"_p50_ms"] = stats.P50
		sr.Extra[cat.Name+"_p95_ms"] = stats.P95
		sr.Extra[cat.Name+"_p99_ms"] = stats.P99
		sr.Extra[cat.Name+"_min_ms"] = stats.Min
		sr.Extra[cat.Name+"_max_ms"] = stats.Max
		sr.Extra[cat.Name+"_mean_ms"] = stats.Mean
	}

	return sr
}
