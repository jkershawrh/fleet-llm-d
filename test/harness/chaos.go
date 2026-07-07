package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RunChaos sends malformed, oversized, and adversarial payloads to verify the
// server handles them gracefully without crashing.
func RunChaos(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "chaos", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	if cfg.Token == "" {
		skip(&sr, "chaos:all", "no token provided")
		return sr
	}

	// --- Test 1: 1MB body ---
	{
		bigBody := strings.Repeat("A", 1<<20) // 1MB
		resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(bigBody))
		if err != nil {
			check(&sr, "chaos:1mb-body", true,
				fmt.Sprintf("connection rejected (expected): %v", err), 0)
		} else {
			drainBody(resp)
			// Server should reject (400/413) or at least not crash (no 5xx).
			check(&sr, "chaos:1mb-body", resp.StatusCode < 500,
				fmt.Sprintf("status=%s (expected 400 or 413)", statusText(resp)), 0)
		}
	}

	// --- Test 2: Invalid JSON ---
	{
		invalidJSON := `{this is not valid json!!!`
		resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(invalidJSON))
		if err != nil {
			check(&sr, "chaos:invalid-json", true,
				fmt.Sprintf("connection error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "chaos:invalid-json", resp.StatusCode == 400 || resp.StatusCode < 500,
				fmt.Sprintf("status=%s", statusText(resp)), 0)
		}
	}

	// --- Test 3: Unicode / emoji in fields ---
	{
		unicodePayload := `{"name":"test-☃-😀","provider":"awsé","region":"eu-ü"}`
		resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(unicodePayload))
		if err != nil {
			check(&sr, "chaos:unicode-emoji", true,
				fmt.Sprintf("connection error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "chaos:unicode-emoji", resp.StatusCode < 500,
				fmt.Sprintf("status=%s", statusText(resp)), 0)
		}
	}

	// --- Test 4: Burst fire 1000 requests ---
	{
		var wg sync.WaitGroup
		var successes, failures int64

		wg.Add(1000)
		for i := 0; i < 1000; i++ {
			go func() {
				defer wg.Done()
				resp, _, err := doRequest("GET", cfg.BaseURL+"/healthz", nil, nil)
				if err != nil {
					atomic.AddInt64(&failures, 1)
					return
				}
				drainBody(resp)
				if statusOK(resp.StatusCode) {
					atomic.AddInt64(&successes, 1)
				} else {
					atomic.AddInt64(&failures, 1)
				}
			}()
		}
		wg.Wait()

		check(&sr, "chaos:burst-1000", successes > 500,
			fmt.Sprintf("successes=%d failures=%d", successes, failures), 0)
	}

	// --- Test 5: Null bytes in body ---
	{
		nullBody := "{\x00\"name\x00\":\x00\"test\x00\"}"
		resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(nullBody))
		if err != nil {
			check(&sr, "chaos:null-bytes", true,
				fmt.Sprintf("connection error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "chaos:null-bytes", resp.StatusCode < 500,
				fmt.Sprintf("status=%s", statusText(resp)), 0)
		}
	}

	// --- Test 6: Empty body to POST endpoints ---
	{
		resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(""))
		if err != nil {
			check(&sr, "chaos:empty-post-body", true,
				fmt.Sprintf("connection error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "chaos:empty-post-body", resp.StatusCode < 500,
				fmt.Sprintf("status=%s", statusText(resp)), 0)
		}
	}

	// --- Test 7: Very long field values ---
	{
		longName := strings.Repeat("x", 10000)
		payload := fmt.Sprintf(`{"name":"%s","provider":"aws","region":"us-east-1"}`, longName)
		resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(payload))
		if err != nil {
			check(&sr, "chaos:long-field-values", true,
				fmt.Sprintf("connection error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "chaos:long-field-values", resp.StatusCode < 500,
				fmt.Sprintf("status=%s", statusText(resp)), 0)
		}
	}

	// --- Test 8: Post-chaos health check ---
	{
		// Wait briefly for server to stabilize.
		time.Sleep(500 * time.Millisecond)
		resp, lat, err := doRequest("GET", cfg.BaseURL+"/healthz", nil, nil)
		if err != nil {
			check(&sr, "chaos:post-chaos-health", false,
				fmt.Sprintf("server unreachable after chaos: %v", err), 0)
		} else {
			body := readBody(resp)
			check(&sr, "chaos:post-chaos-health", statusOK(resp.StatusCode),
				fmt.Sprintf("status=%s body=%s", statusText(resp), truncate(body, 80)),
				lat.Milliseconds())
		}
	}

	return sr
}
