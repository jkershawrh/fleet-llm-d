package main

import (
	"fmt"
	"sync"
	"time"
)

// RunStress ramps concurrent goroutines through increasing levels and measures
// latency percentiles at each level to find the breaking point.
func RunStress(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "stress", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	levels := []int{1, 10, 50, 100, 200, 500}
	requestsPerGoroutine := 10
	breakingPoint := -1

	for _, concurrency := range levels {
		var mu sync.Mutex
		var latencies []time.Duration
		var errors int

		var wg sync.WaitGroup
		wg.Add(concurrency)

		for g := 0; g < concurrency; g++ {
			go func() {
				defer wg.Done()
				for i := 0; i < requestsPerGoroutine; i++ {
					resp, lat, err := doRequest("GET", cfg.BaseURL+"/healthz", nil, nil)
					if err != nil {
						mu.Lock()
						errors++
						mu.Unlock()
						continue
					}
					drainBody(resp)
					mu.Lock()
					if statusOK(resp.StatusCode) {
						latencies = append(latencies, lat)
					} else {
						errors++
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		total := concurrency * requestsPerGoroutine
		errorRate := float64(errors) / float64(total) * 100.0
		stats := computeLatencyStats(latencies)

		name := fmt.Sprintf("stress:%d-goroutines", concurrency)
		detail := fmt.Sprintf("total=%d ok=%d errors=%d error_rate=%.1f%% p50=%.1fms p95=%.1fms p99=%.1fms",
			total, len(latencies), errors, errorRate,
			stats.P50, stats.P95, stats.P99)

		passed := errorRate < 10.0 // less than 10% error rate
		check(&sr, name, passed, detail, int64(stats.P50))

		sr.Extra[fmt.Sprintf("level_%d_p50_ms", concurrency)] = stats.P50
		sr.Extra[fmt.Sprintf("level_%d_p95_ms", concurrency)] = stats.P95
		sr.Extra[fmt.Sprintf("level_%d_p99_ms", concurrency)] = stats.P99
		sr.Extra[fmt.Sprintf("level_%d_error_rate", concurrency)] = errorRate

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
