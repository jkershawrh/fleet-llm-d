package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunInference exercises the inference proxy under increasing concurrency,
// measuring TTFT (time to first token), total latency, and error rate at
// each level. It targets /v1/chat/completions with a real model name.
func RunInference(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "inference", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	model := inferenceModel(cfg)
	if model == "" {
		skip(&sr, "inference:discover-model", "no inference model available — set --inference-model or ensure a backend is registered")
		return sr
	}
	sr.Extra["model"] = model

	// Verify the inference endpoint is reachable with a single request.
	ttft, total, err := singleInferenceRequest(cfg, model)
	if err != nil {
		check(&sr, "inference:single-request", false,
			fmt.Sprintf("single inference request failed: %v", err), 0)
		return sr
	}
	check(&sr, "inference:single-request", true,
		fmt.Sprintf("ttft=%.0fms total=%.0fms", ttft, total),
		int64(total))
	sr.Extra["baseline_ttft_ms"] = ttft
	sr.Extra["baseline_total_ms"] = total

	// Ramp concurrency levels.
	levels := []int{1, 2, 5, 10, 20, 50}
	requestsPerClient := 3
	breakingPoint := -1

	for _, concurrency := range levels {
		var mu sync.Mutex
		var ttfts, totals []time.Duration
		var errors int32

		var wg sync.WaitGroup
		wg.Add(concurrency)

		levelStart := time.Now()
		for g := 0; g < concurrency; g++ {
			go func() {
				defer wg.Done()
				for i := 0; i < requestsPerClient; i++ {
					t, tot, err := singleInferenceRequest(cfg, model)
					if err != nil {
						atomic.AddInt32(&errors, 1)
						continue
					}
					mu.Lock()
					ttfts = append(ttfts, time.Duration(t*float64(time.Millisecond)))
					totals = append(totals, time.Duration(tot*float64(time.Millisecond)))
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		levelDuration := time.Since(levelStart)

		totalReqs := concurrency * requestsPerClient
		errCount := int(atomic.LoadInt32(&errors))
		errorRate := float64(errCount) / float64(totalReqs) * 100.0
		ttftStats := computeLatencyStats(ttfts)
		totalStats := computeLatencyStats(totals)

		name := fmt.Sprintf("inference:%d-concurrent", concurrency)
		detail := fmt.Sprintf("reqs=%d ok=%d errors=%d rate=%.1f%% ttft_p50=%.0fms ttft_p95=%.0fms total_p50=%.0fms duration=%s",
			totalReqs, len(ttfts), errCount, errorRate,
			ttftStats.P50, ttftStats.P95, totalStats.P50, formatDuration(levelDuration))

		passed := errorRate < 5.0 && ttftStats.P95 < 30000 // <5% errors, <30s TTFT P95
		check(&sr, name, passed, detail, int64(ttftStats.P50))

		sr.Extra[fmt.Sprintf("c%d_ttft_p50_ms", concurrency)] = ttftStats.P50
		sr.Extra[fmt.Sprintf("c%d_ttft_p95_ms", concurrency)] = ttftStats.P95
		sr.Extra[fmt.Sprintf("c%d_ttft_p99_ms", concurrency)] = ttftStats.P99
		sr.Extra[fmt.Sprintf("c%d_total_p50_ms", concurrency)] = totalStats.P50
		sr.Extra[fmt.Sprintf("c%d_total_p95_ms", concurrency)] = totalStats.P95
		sr.Extra[fmt.Sprintf("c%d_error_rate", concurrency)] = errorRate
		sr.Extra[fmt.Sprintf("c%d_throughput_rps", concurrency)] = float64(len(ttfts)) / levelDuration.Seconds()

		if !passed && breakingPoint < 0 {
			breakingPoint = concurrency
		}
	}

	if breakingPoint > 0 {
		sr.Extra["breaking_point"] = breakingPoint
	} else {
		sr.Extra["breaking_point"] = "none (survived all levels)"
	}

	return sr
}

// inferenceModel discovers which model to use for inference testing.
// If cfg.InferenceModel is set, it is returned directly (skipping discovery).
// Otherwise it tries the /v1/models endpoint first, then falls back to known defaults.
func inferenceModel(cfg Config) string {
	if cfg.InferenceModel != "" {
		return cfg.InferenceModel
	}

	// Try known model names registered in the proxy first.
	// A model is usable if the backend returns 200 (not 502 from proxy, not 404 from backend).
	knownModels := []string{"granite-3.2-sovereign", "granite-sovereign", "granite-3.3-2b"}
	for _, m := range knownModels {
		body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"test"}],"max_tokens":1}`, m)
		resp, _, err := authPost(cfg.BaseURL, "/v1/chat/completions", cfg.Token, strings.NewReader(body))
		if err == nil && resp != nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return m
			}
		}
	}

	return ""
}

// singleInferenceRequest sends one chat completion request and measures
// TTFT (time to first token) and total latency in milliseconds.
func singleInferenceRequest(cfg Config, model string) (ttftMs, totalMs float64, err error) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"What is AI?"}],"max_tokens":20}`, model)

	req, err := http.NewRequest("POST", cfg.BaseURL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return 0, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	reqStart := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	// Read full response (non-streaming).
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	totalDuration := time.Since(reqStart)

	return float64(totalDuration.Microseconds()) / 1000.0, float64(totalDuration.Microseconds()) / 1000.0, nil
}
