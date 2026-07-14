package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type scaleLevelResult struct {
	ClusterCount       int
	RegistrationTimeMs float64
	ListLatency        *LatencyStats
	HealthzLatency     *LatencyStats
	ReconcileTimeMs    float64
	MemoryBeforeMB     float64
	MemoryAfterMB      float64
	MemoryDeltaMB      float64
}

func RunScale(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "scale", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	if cfg.Token == "" && cfg.Secret == "" {
		skip(&sr, "scale:all", "no token or secret provided")
		return sr
	}

	levels := []int{10, 50, 100, 250, 500, 1000}
	results := make([]scaleLevelResult, 0, len(levels))

	getMemoryMB := func() float64 {
		resp, _, err := doRequest("GET", cfg.BaseURL+"/debug/vars", nil, nil)
		if err != nil {
			return 0
		}
		body := readBody(resp)
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

	for _, n := range levels {
		fmt.Printf("\n  --- Scale level: %d clusters ---\n", n)
		lr := scaleLevelResult{ClusterCount: n}

		// 1. Memory before
		lr.MemoryBeforeMB = getMemoryMB()

		// 2. Bulk register N clusters concurrently
		var registerWG sync.WaitGroup
		var regSuccess, regFailure int64
		regStart := time.Now()

		registerWG.Add(n)
		for i := 0; i < n; i++ {
			go func(idx int) {
				defer registerWG.Done()
				name := fmt.Sprintf("scale-%d-%d", n, idx)
				resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
					strings.NewReader(clusterPayload(name)))
				if err != nil {
					atomic.AddInt64(&regFailure, 1)
					return
				}
				drainBody(resp)
				if statusOK(resp.StatusCode) || resp.StatusCode == 201 {
					atomic.AddInt64(&regSuccess, 1)
				} else {
					atomic.AddInt64(&regFailure, 1)
				}
			}(i)
		}
		registerWG.Wait()
		lr.RegistrationTimeMs = float64(time.Since(regStart).Milliseconds())

		check(&sr, fmt.Sprintf("scale:%d-register", n),
			regSuccess >= int64(n)*9/10,
			fmt.Sprintf("registered %d/%d in %.0fms (%.0f/s)",
				regSuccess, n, lr.RegistrationTimeMs,
				float64(regSuccess)/time.Since(regStart).Seconds()),
			int64(lr.RegistrationTimeMs))

		// 3. List latency (100 sequential GET /api/v1/clusters)
		var listLatencies []time.Duration
		for i := 0; i < 100; i++ {
			resp, lat, err := authGet(cfg.BaseURL, "/api/v1/clusters", cfg.Token)
			if err != nil {
				continue
			}
			drainBody(resp)
			if statusOK(resp.StatusCode) {
				listLatencies = append(listLatencies, lat)
			}
		}
		if len(listLatencies) > 0 {
			lr.ListLatency = computeLatencyStats(listLatencies)
			check(&sr, fmt.Sprintf("scale:%d-list-latency", n),
				lr.ListLatency.P95 < float64(n)/2+50,
				fmt.Sprintf("p50=%.1fms p95=%.1fms p99=%.1fms (%d samples)",
					lr.ListLatency.P50, lr.ListLatency.P95, lr.ListLatency.P99, len(listLatencies)),
				int64(lr.ListLatency.P50))
		}

		// 4. Healthz latency (100 sequential, should be constant)
		var healthzLatencies []time.Duration
		for i := 0; i < 100; i++ {
			resp, lat, err := doRequest("GET", cfg.BaseURL+"/healthz", nil, nil)
			if err != nil {
				continue
			}
			drainBody(resp)
			if statusOK(resp.StatusCode) {
				healthzLatencies = append(healthzLatencies, lat)
			}
		}
		if len(healthzLatencies) > 0 {
			lr.HealthzLatency = computeLatencyStats(healthzLatencies)
			check(&sr, fmt.Sprintf("scale:%d-healthz", n),
				lr.HealthzLatency.P95 < 10.0,
				fmt.Sprintf("p50=%.1fms p95=%.1fms (should be <10ms regardless of cluster count)",
					lr.HealthzLatency.P50, lr.HealthzLatency.P95),
				int64(lr.HealthzLatency.P50))
		}

		// 5. Reconciliation (create 5 pools, measure wall time)
		reconcileStart := time.Now()
		for p := 0; p < 5; p++ {
			poolPayload := fmt.Sprintf(`{"type":"ADDED","object":{"apiVersion":"fleet.llm-d.ai/v1alpha1","kind":"FleetInferencePool","metadata":{"name":"scale-pool-%d"},"spec":{"model":{"name":"scale-model-%d","source":"hf://test/scale-%d"},"placement":{"policyRef":"default","maxClusters":10},"serving":{"inferencePoolTemplate":{"spec":{"targetPorts":[8080]}}}}}}`, p, p, p)
			resp, _, err := authPost(cfg.BaseURL, "/api/v1/webhook/fleetinferencepool", cfg.Token,
				strings.NewReader(poolPayload))
			if err == nil {
				drainBody(resp)
			}
		}
		lr.ReconcileTimeMs = float64(time.Since(reconcileStart).Milliseconds())

		check(&sr, fmt.Sprintf("scale:%d-reconcile", n),
			lr.ReconcileTimeMs < float64(n)*10+1000,
			fmt.Sprintf("5 pools reconciled in %.0fms (%.0fms/pool)",
				lr.ReconcileTimeMs, lr.ReconcileTimeMs/5),
			int64(lr.ReconcileTimeMs))

		// 6. Memory after
		lr.MemoryAfterMB = getMemoryMB()
		lr.MemoryDeltaMB = lr.MemoryAfterMB - lr.MemoryBeforeMB
		if lr.MemoryBeforeMB > 0 {
			check(&sr, fmt.Sprintf("scale:%d-memory", n),
				lr.MemoryDeltaMB < float64(n)/2+50,
				fmt.Sprintf("before=%.1fMB after=%.1fMB delta=%.1fMB",
					lr.MemoryBeforeMB, lr.MemoryAfterMB, lr.MemoryDeltaMB),
				0)
		}

		results = append(results, lr)

		// Store per-level data
		sr.Extra[fmt.Sprintf("level_%d_reg_ms", n)] = lr.RegistrationTimeMs
		if lr.ListLatency != nil {
			sr.Extra[fmt.Sprintf("level_%d_list_p50_ms", n)] = lr.ListLatency.P50
			sr.Extra[fmt.Sprintf("level_%d_list_p95_ms", n)] = lr.ListLatency.P95
		}
		if lr.HealthzLatency != nil {
			sr.Extra[fmt.Sprintf("level_%d_healthz_p50_ms", n)] = lr.HealthzLatency.P50
		}
		sr.Extra[fmt.Sprintf("level_%d_reconcile_ms", n)] = lr.ReconcileTimeMs
		sr.Extra[fmt.Sprintf("level_%d_memory_mb", n)] = lr.MemoryDeltaMB

		// 7. Cleanup: delete all scale-N-* clusters
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("scale-%d-%d", n, i)
			resp, _, err := authDelete(cfg.BaseURL, "/api/v1/clusters/"+name, cfg.Token)
			if err == nil {
				drainBody(resp)
			}
		}
		// Delete test pools
		for p := 0; p < 5; p++ {
			poolPayload := fmt.Sprintf(`{"type":"DELETED","object":{"apiVersion":"fleet.llm-d.ai/v1alpha1","kind":"FleetInferencePool","metadata":{"name":"scale-pool-%d"},"spec":{"model":{"name":"scale-model-%d","source":"hf://test/scale-%d"},"placement":{"policyRef":"default","maxClusters":10},"serving":{"inferencePoolTemplate":{"spec":{"targetPorts":[8080]}}}}}}`, p, p, p)
			resp, _, err := authPost(cfg.BaseURL, "/api/v1/webhook/fleetinferencepool", cfg.Token,
				strings.NewReader(poolPayload))
			if err == nil {
				drainBody(resp)
			}
		}

		fmt.Printf("    Reg: %.0fms  List p50/p95: ", lr.RegistrationTimeMs)
		if lr.ListLatency != nil {
			fmt.Printf("%.1f/%.1fms", lr.ListLatency.P50, lr.ListLatency.P95)
		} else {
			fmt.Printf("N/A")
		}
		fmt.Printf("  Reconcile: %.0fms  Memory: %.1fMB\n", lr.ReconcileTimeMs, lr.MemoryDeltaMB)
	}

	// Print scaling curve summary
	fmt.Printf("\n  FLEET SCALE BENCHMARK — Scaling Curve\n")
	fmt.Printf("  %-10s %-12s %-14s %-14s %-14s %-10s\n",
		"Clusters", "Reg(ms)", "List p50(ms)", "List p95(ms)", "Reconcile(ms)", "Mem(MB)")
	fmt.Printf("  %s\n", strings.Repeat("-", 74))
	for _, lr := range results {
		listP50, listP95 := 0.0, 0.0
		if lr.ListLatency != nil {
			listP50 = lr.ListLatency.P50
			listP95 = lr.ListLatency.P95
		}
		fmt.Printf("  %-10d %-12.0f %-14.1f %-14.1f %-14.0f %-10.1f\n",
			lr.ClusterCount, lr.RegistrationTimeMs, listP50, listP95,
			lr.ReconcileTimeMs, lr.MemoryDeltaMB)
	}

	// Detect knee point
	if len(results) >= 2 {
		kneePoint := "none"
		for i := 1; i < len(results); i++ {
			inputGrowth := float64(results[i].ClusterCount) / float64(results[i-1].ClusterCount)
			if results[i].ListLatency != nil && results[i-1].ListLatency != nil &&
				results[i-1].ListLatency.P95 > 0 {
				metricGrowth := results[i].ListLatency.P95 / results[i-1].ListLatency.P95
				if metricGrowth > inputGrowth*2.5 {
					kneePoint = fmt.Sprintf("%d clusters (List p95 grew %.1fx for %.1fx input)",
						results[i].ClusterCount, metricGrowth, inputGrowth)
					break
				}
			}
		}
		sr.Extra["knee_point"] = kneePoint
		fmt.Printf("\n  Knee point: %s\n", kneePoint)
	}

	return sr
}
