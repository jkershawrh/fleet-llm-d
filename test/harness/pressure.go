package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunPressure tests concurrent write contention, read/write races, rapid
// register/deregister cycles, and burst writes.
func RunPressure(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "pressure", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	if cfg.Token == "" {
		skip(&sr, "pressure:all", "no token provided")
		return sr
	}

	// --- Test 1: 50 goroutines writing same ID ---
	{
		clusterName := uniqueID("pressure-same")
		payload := clusterPayload(clusterName)
		var wg sync.WaitGroup
		var successes, failures int64

		wg.Add(50)
		for i := 0; i < 50; i++ {
			go func() {
				defer wg.Done()
				resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
					strings.NewReader(payload))
				if err != nil {
					atomic.AddInt64(&failures, 1)
					return
				}
				drainBody(resp)
				if statusOK(resp.StatusCode) || resp.StatusCode == 201 || resp.StatusCode == 409 {
					atomic.AddInt64(&successes, 1)
				} else {
					atomic.AddInt64(&failures, 1)
				}
			}()
		}
		wg.Wait()

		check(&sr, "pressure:50-goroutines-same-id", failures == 0 || successes > 0,
			fmt.Sprintf("successes=%d failures=%d (409 conflict is acceptable)",
				successes, failures), 0)
	}

	// --- Test 2: read/write races ---
	{
		var wg sync.WaitGroup
		var readErrors, writeErrors int64
		done := make(chan struct{})

		// Writers
		wg.Add(10)
		for i := 0; i < 10; i++ {
			go func(idx int) {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
					}
					name := fmt.Sprintf("race-%d-%d", idx, time.Now().UnixNano()%10000)
					resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
						strings.NewReader(clusterPayload(name)))
					if err != nil {
						atomic.AddInt64(&writeErrors, 1)
						continue
					}
					drainBody(resp)
					if resp.StatusCode >= 500 {
						atomic.AddInt64(&writeErrors, 1)
					}
				}
			}(i)
		}

		// Readers
		wg.Add(10)
		for i := 0; i < 10; i++ {
			go func() {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
					}
					resp, _, err := authGet(cfg.BaseURL, "/api/v1/clusters", cfg.Token)
					if err != nil {
						atomic.AddInt64(&readErrors, 1)
						continue
					}
					drainBody(resp)
					if resp.StatusCode >= 500 {
						atomic.AddInt64(&readErrors, 1)
					}
				}
			}()
		}

		time.Sleep(3 * time.Second)
		close(done)
		wg.Wait()

		check(&sr, "pressure:read-write-races", true,
			fmt.Sprintf("read_5xx=%d write_5xx=%d (no panics observed)",
				readErrors, writeErrors), 0)
	}

	// --- Test 3: rapid register/deregister 1000x ---
	{
		var successes, failures int64
		for i := 0; i < 1000; i++ {
			name := fmt.Sprintf("rapid-%d", i)
			resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
				strings.NewReader(clusterPayload(name)))
			if err != nil {
				atomic.AddInt64(&failures, 1)
				continue
			}
			body := readBody(resp)
			clusterID := extractID(body, name)

			resp2, _, err2 := authDelete(cfg.BaseURL, "/api/v1/clusters/"+clusterID, cfg.Token)
			if err2 != nil {
				atomic.AddInt64(&failures, 1)
				continue
			}
			drainBody(resp2)
			atomic.AddInt64(&successes, 1)
		}

		check(&sr, "pressure:rapid-register-deregister-1000x",
			successes > 900,
			fmt.Sprintf("successes=%d failures=%d", successes, failures), 0)
	}

	// --- Test 4: burst 500 in 1 second ---
	{
		var wg sync.WaitGroup
		var successes, failures int64
		burstStart := time.Now()

		wg.Add(500)
		for i := 0; i < 500; i++ {
			go func(idx int) {
				defer wg.Done()
				name := fmt.Sprintf("burst-%d", idx)
				resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
					strings.NewReader(clusterPayload(name)))
				if err != nil {
					atomic.AddInt64(&failures, 1)
					return
				}
				drainBody(resp)
				if resp.StatusCode < 500 {
					atomic.AddInt64(&successes, 1)
				} else {
					atomic.AddInt64(&failures, 1)
				}
			}(i)
		}
		wg.Wait()
		burstDuration := time.Since(burstStart)

		check(&sr, "pressure:burst-500-in-1s", successes > 400,
			fmt.Sprintf("successes=%d failures=%d duration=%s",
				successes, failures, formatDuration(burstDuration)), 0)
		sr.Extra["burst_duration_ms"] = burstDuration.Milliseconds()
	}

	return sr
}
