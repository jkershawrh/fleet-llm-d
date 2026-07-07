package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunThroughput uses binary search to find the maximum sustained request rate
// where p99 latency stays below a target threshold.
func RunThroughput(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "throughput", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	targetP99ms := 500.0 // p99 must stay under 500ms
	testDuration := 5 * time.Second

	type endpoint struct {
		Name    string
		Method  string
		Path    string
		Auth    bool
		HasBody bool
	}

	endpoints := []endpoint{
		{"healthz", "GET", "/healthz", false, false},
		{"get-clusters", "GET", "/api/v1/clusters", true, false},
		{"post-clusters", "POST", "/api/v1/clusters", true, true},
	}

	for _, ep := range endpoints {
		if ep.Auth && cfg.Token == "" {
			skip(&sr, "throughput:"+ep.Name, "no token provided")
			continue
		}

		maxRPS := binarySearchRPS(cfg, ep.Method, ep.Path, ep.Auth, ep.HasBody,
			targetP99ms, testDuration)

		detail := fmt.Sprintf("max_sustained_rps=%d (p99 < %.0fms)", maxRPS, targetP99ms)
		check(&sr, "throughput:"+ep.Name, maxRPS > 0, detail, 0)
		sr.Extra[ep.Name+"_max_rps"] = maxRPS
	}

	return sr
}

// binarySearchRPS finds the highest request rate where p99 stays under the target.
func binarySearchRPS(cfg Config, method, path string, auth, hasBody bool,
	targetP99ms float64, testDuration time.Duration) int {

	low := 1
	high := 2000
	best := 0

	for low <= high {
		mid := (low + high) / 2
		p99 := measureP99AtRate(cfg, method, path, auth, hasBody, mid, testDuration)

		if p99 <= targetP99ms && p99 >= 0 {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	return best
}

// measureP99AtRate fires requests at the given rate for the duration and returns
// the p99 latency in milliseconds. Returns -1 if too many errors.
func measureP99AtRate(cfg Config, method, path string, auth, hasBody bool,
	rps int, duration time.Duration) float64 {

	if rps <= 0 {
		return -1
	}

	interval := time.Second / time.Duration(rps)
	var mu sync.Mutex
	var latencies []time.Duration
	var totalErrors int64

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.After(duration)

	var wg sync.WaitGroup

	func() {
		for {
			select {
			case <-deadline:
				return
			case <-ticker.C:
				wg.Add(1)
				go func() {
					defer wg.Done()
					var resp *http.Response
					var lat time.Duration
					var err error

					if !auth {
						resp, lat, err = doRequest(method, cfg.BaseURL+path, nil, nil)
					} else if hasBody {
						body := clusterPayload(uniqueID("tp"))
						resp, lat, err = authPost(cfg.BaseURL, path, cfg.Token,
							strings.NewReader(body))
					} else {
						resp, lat, err = authGet(cfg.BaseURL, path, cfg.Token)
					}

					if err != nil {
						atomic.AddInt64(&totalErrors, 1)
						return
					}
					drainBody(resp)
					mu.Lock()
					latencies = append(latencies, lat)
					mu.Unlock()
				}()
			}
		}
	}()

	wg.Wait()

	total := len(latencies) + int(totalErrors)
	if total == 0 {
		return -1
	}
	errorRate := float64(totalErrors) / float64(total)
	if errorRate > 0.1 {
		return -1 // too many errors
	}

	stats := computeLatencyStats(latencies)
	return stats.P99
}
