package main

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunMultiModel sends concurrent inference requests spread across multiple
// models to simulate a real Summit Connect lab environment where different
// users hit different models simultaneously.
func RunMultiModel(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "multimodel", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	models := parseModels(cfg)
	if len(models) == 0 {
		skip(&sr, "multimodel:discover", "no models — set --inference-models=model1,model2,...")
		return sr
	}
	sr.Extra["models"] = strings.Join(models, ",")
	sr.Extra["model_count"] = len(models)

	levels := []int{3, 6, 10, 15, 20, 30}
	requestsPerClient := 2
	breakingPoint := -1

	for _, concurrency := range levels {
		var mu sync.Mutex
		var ttfts []time.Duration
		var errors int32
		perModel := make(map[string]int)

		var wg sync.WaitGroup
		wg.Add(concurrency)

		levelStart := time.Now()
		for g := 0; g < concurrency; g++ {
			model := models[g%len(models)]
			go func(m string) {
				defer wg.Done()
				for i := 0; i < requestsPerClient; i++ {
					t, _, err := singleInferenceRequest(cfg, m)
					if err != nil {
						atomic.AddInt32(&errors, 1)
						continue
					}
					mu.Lock()
					ttfts = append(ttfts, time.Duration(t*float64(time.Millisecond)))
					perModel[m]++
					mu.Unlock()
				}
			}(model)
		}
		wg.Wait()
		levelDuration := time.Since(levelStart)

		totalReqs := concurrency * requestsPerClient
		errCount := int(atomic.LoadInt32(&errors))
		errorRate := float64(errCount) / float64(totalReqs) * 100.0
		ttftStats := computeLatencyStats(ttfts)

		name := fmt.Sprintf("multimodel:%d-concurrent", concurrency)
		detail := fmt.Sprintf("models=%d reqs=%d ok=%d errors=%d rate=%.1f%% ttft_p50=%.0fms ttft_p95=%.0fms duration=%s",
			len(models), totalReqs, len(ttfts), errCount, errorRate,
			ttftStats.P50, ttftStats.P95, formatDuration(levelDuration))

		passed := errorRate < 10.0 && ttftStats.P95 < 30000
		check(&sr, name, passed, detail, int64(ttftStats.P50))

		sr.Extra[fmt.Sprintf("mm%d_ttft_p50_ms", concurrency)] = ttftStats.P50
		sr.Extra[fmt.Sprintf("mm%d_ttft_p95_ms", concurrency)] = ttftStats.P95
		sr.Extra[fmt.Sprintf("mm%d_error_rate", concurrency)] = errorRate
		sr.Extra[fmt.Sprintf("mm%d_throughput_rps", concurrency)] = float64(len(ttfts)) / levelDuration.Seconds()

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

// RunFairness tests per-tenant rate limiting by simulating multiple lab
// sessions where one "greedy" session sends requests much faster than others.
func RunFairness(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "fairness", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	models := parseModels(cfg)
	if len(models) == 0 {
		skip(&sr, "fairness:discover", "no models — set --inference-models=model1,model2,...")
		return sr
	}
	model := models[0]
	sr.Extra["model"] = model

	// Simulate 4 normal sessions + 1 greedy session over 30 seconds
	testDuration := 30 * time.Second
	normalRate := 500 * time.Millisecond  // 1 req every 500ms = 2 rps
	greedyRate := 50 * time.Millisecond   // 1 req every 50ms = 20 rps

	type sessionResult struct {
		name     string
		success  int32
		errors   int32
		rejected int32 // 429s
		ttfts    []time.Duration
	}

	sessions := []struct {
		name     string
		interval time.Duration
		tenantID string
	}{
		{"normal-1", normalRate, "lab-session-1"},
		{"normal-2", normalRate, "lab-session-2"},
		{"normal-3", normalRate, "lab-session-3"},
		{"normal-4", normalRate, "lab-session-4"},
		{"greedy", greedyRate, "lab-session-greedy"},
	}

	results := make([]sessionResult, len(sessions))
	var wg sync.WaitGroup

	for i, sess := range sessions {
		wg.Add(1)
		go func(idx int, s struct {
			name     string
			interval time.Duration
			tenantID string
		}) {
			defer wg.Done()
			r := &results[idx]
			r.name = s.name
			deadline := time.After(testDuration)

			for {
				select {
				case <-deadline:
					return
				default:
				}

				body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Hi"}],"max_tokens":10}`, model)
				headers := map[string]string{
					"Content-Type":                    "application/json",
					"Authorization":                   "Bearer " + cfg.Token,
					"x-llm-d-inference-fairness-id":   s.tenantID,
				}
				resp, lat, err := doRequest("POST", cfg.BaseURL+"/v1/chat/completions", strings.NewReader(body), headers)
				if err != nil {
					atomic.AddInt32(&r.errors, 1)
				} else if resp.StatusCode == 429 {
					atomic.AddInt32(&r.rejected, 1)
					drainBody(resp)
				} else if resp.StatusCode == 200 {
					atomic.AddInt32(&r.success, 1)
					r.ttfts = append(r.ttfts, lat)
					drainBody(resp)
				} else {
					atomic.AddInt32(&r.errors, 1)
					drainBody(resp)
				}

				time.Sleep(s.interval)
			}
		}(i, sess)
	}
	wg.Wait()

	// Evaluate results
	greedyRejected := int(results[4].rejected)
	greedyTotal := int(results[4].success) + int(results[4].errors) + greedyRejected

	check(&sr, "fairness:greedy-gets-throttled",
		greedyRejected > 0,
		fmt.Sprintf("greedy sent %d reqs, %d rejected (429)", greedyTotal, greedyRejected),
		0)

	normalOK := true
	for i := 0; i < 4; i++ {
		r := results[i]
		total := int(r.success) + int(r.errors) + int(r.rejected)
		errRate := float64(r.errors) / float64(max(total, 1)) * 100.0
		stats := computeLatencyStats(r.ttfts)

		passed := errRate < 20.0
		if !passed {
			normalOK = false
		}

		check(&sr, fmt.Sprintf("fairness:%s", r.name), passed,
			fmt.Sprintf("sent=%d ok=%d err=%d rejected=%d errRate=%.0f%% ttft_p50=%.0fms",
				total, r.success, r.errors, r.rejected, errRate, stats.P50),
			int64(stats.P50))

		sr.Extra[fmt.Sprintf("%s_success", r.name)] = r.success
		sr.Extra[fmt.Sprintf("%s_rejected", r.name)] = r.rejected
		sr.Extra[fmt.Sprintf("%s_errors", r.name)] = r.errors
	}

	check(&sr, "fairness:normal-sessions-unaffected", normalOK,
		"all normal sessions maintained acceptable error rates", 0)

	sr.Extra["greedy_rejected"] = greedyRejected
	sr.Extra["greedy_total"] = greedyTotal

	return sr
}

func parseModels(cfg Config) []string {
	if cfg.InferenceModels != "" {
		var models []string
		for _, m := range strings.Split(cfg.InferenceModels, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				models = append(models, m)
			}
		}
		return models
	}
	if cfg.InferenceModel != "" {
		return []string{cfg.InferenceModel}
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// init seeds the random number generator.
func init() {
	rand.Seed(time.Now().UnixNano())
}
