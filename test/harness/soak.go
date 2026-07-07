package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunSoak performs a sustained mixed workload at ~10 req/s for the configured
// duration, taking snapshots every 60s with memory tracking from /metrics.
func RunSoak(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "soak", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	duration := cfg.Duration
	if duration == 0 {
		duration = 5 * time.Minute
	}

	targetRPS := 10
	interval := time.Second / time.Duration(targetRPS)
	snapshotInterval := 60 * time.Second

	var mu sync.Mutex
	var allLatencies []time.Duration
	var totalRequests int64
	var totalErrors int64

	type snapshot struct {
		Time      string  `json:"time"`
		Requests  int64   `json:"requests"`
		Errors    int64   `json:"errors"`
		ErrorRate float64 `json:"error_rate_pct"`
		P50ms     float64 `json:"p50_ms"`
		P95ms     float64 `json:"p95_ms"`
		MemoryMB  float64 `json:"memory_mb,omitempty"`
	}
	var snapshots []snapshot

	// Mixed workload: health, auth reads, auth writes.
	workload := func() {
		ops := []func(){
			func() {
				resp, lat, err := doRequest("GET", cfg.BaseURL+"/healthz", nil, nil)
				if err != nil {
					atomic.AddInt64(&totalErrors, 1)
					return
				}
				drainBody(resp)
				if !statusOK(resp.StatusCode) {
					atomic.AddInt64(&totalErrors, 1)
					return
				}
				mu.Lock()
				allLatencies = append(allLatencies, lat)
				mu.Unlock()
			},
			func() {
				if cfg.Token == "" {
					return
				}
				resp, lat, err := authGet(cfg.BaseURL, "/api/v1/clusters", cfg.Token)
				if err != nil {
					atomic.AddInt64(&totalErrors, 1)
					return
				}
				drainBody(resp)
				if !statusOK(resp.StatusCode) {
					atomic.AddInt64(&totalErrors, 1)
					return
				}
				mu.Lock()
				allLatencies = append(allLatencies, lat)
				mu.Unlock()
			},
			func() {
				if cfg.Token == "" {
					return
				}
				body := clusterPayload(uniqueID("soak"))
				resp, lat, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
					strings.NewReader(body))
				if err != nil {
					atomic.AddInt64(&totalErrors, 1)
					return
				}
				drainBody(resp)
				mu.Lock()
				allLatencies = append(allLatencies, lat)
				mu.Unlock()
			},
		}

		idx := int(atomic.LoadInt64(&totalRequests)) % len(ops)
		atomic.AddInt64(&totalRequests, 1)
		ops[idx]()
	}

	// Take memory snapshot from metrics endpoint.
	getMemoryMB := func() float64 {
		if cfg.MetricsURL == "" {
			return 0
		}
		resp, _, err := doRequest("GET", cfg.MetricsURL+"/metrics", nil, nil)
		if err != nil {
			return 0
		}
		body := readBody(resp)
		// Try to parse as JSON expvar format.
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(body), &data); err == nil {
			if memstats, ok := data["memstats"].(map[string]interface{}); ok {
				if alloc, ok := memstats["Alloc"].(float64); ok {
					return alloc / (1024 * 1024)
				}
			}
		}
		return 0
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	snapshotTicker := time.NewTicker(snapshotInterval)
	defer snapshotTicker.Stop()

	deadline := time.After(duration)
	snapshotCount := 0

	for {
		select {
		case <-deadline:
			goto done
		case <-snapshotTicker.C:
			snapshotCount++
			reqs := atomic.LoadInt64(&totalRequests)
			errs := atomic.LoadInt64(&totalErrors)
			errRate := 0.0
			if reqs > 0 {
				errRate = float64(errs) / float64(reqs) * 100.0
			}
			mu.Lock()
			stats := computeLatencyStats(allLatencies)
			mu.Unlock()
			memMB := getMemoryMB()
			snap := snapshot{
				Time:      time.Now().UTC().Format(time.RFC3339),
				Requests:  reqs,
				Errors:    errs,
				ErrorRate: errRate,
				P50ms:     stats.P50,
				P95ms:     stats.P95,
				MemoryMB:  memMB,
			}
			snapshots = append(snapshots, snap)

			name := fmt.Sprintf("soak:snapshot-%d", snapshotCount)
			check(&sr, name, errRate < 5.0,
				fmt.Sprintf("reqs=%d errs=%d rate=%.1f%% p50=%.1fms p95=%.1fms mem=%.1fMB",
					reqs, errs, errRate, stats.P50, stats.P95, memMB),
				int64(stats.P50))

		case <-ticker.C:
			go workload()
		}
	}

done:
	// Final stats.
	reqs := atomic.LoadInt64(&totalRequests)
	errs := atomic.LoadInt64(&totalErrors)
	errRate := 0.0
	if reqs > 0 {
		errRate = float64(errs) / float64(reqs) * 100.0
	}
	mu.Lock()
	stats := computeLatencyStats(allLatencies)
	mu.Unlock()

	check(&sr, "soak:final-error-rate", errRate < 5.0,
		fmt.Sprintf("total_reqs=%d total_errs=%d error_rate=%.2f%%", reqs, errs, errRate), 0)

	sr.Latencies = stats
	sr.Extra["total_requests"] = reqs
	sr.Extra["total_errors"] = errs
	sr.Extra["duration_s"] = duration.Seconds()
	sr.Extra["target_rps"] = targetRPS
	sr.Extra["snapshot_count"] = len(snapshots)

	if len(snapshots) >= 2 {
		first := snapshots[0].MemoryMB
		last := snapshots[len(snapshots)-1].MemoryMB
		if first > 0 && last > 0 {
			growth := last - first
			sr.Extra["memory_growth_mb"] = growth
			check(&sr, "soak:memory-stability", growth < 100,
				fmt.Sprintf("first=%.1fMB last=%.1fMB growth=%.1fMB", first, last, growth), 0)
		}
	}

	return sr
}
