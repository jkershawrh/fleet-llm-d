package main

import (
	"fmt"
	"strings"
	"time"
)

// runSteadyPhase sends health-check requests at ~2 rps for the given duration
// and returns the total request count and error count.
func runSteadyPhase(cfg Config, duration time.Duration) (total, errors int) {
	deadline := time.After(duration)
	for {
		select {
		case <-deadline:
			return
		default:
		}
		resp, _, err := authGet(cfg.BaseURL, "/healthz", cfg.Token)
		total++
		if err != nil || resp == nil || resp.StatusCode != 200 {
			errors++
		}
		if resp != nil {
			drainBody(resp)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// RunChaosRecovery runs a sustained workload, simulates a backend failure
// (by sending requests to a non-existent model to trigger health polling),
// and verifies the system recovers gracefully.
func RunChaosRecovery(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "chaos-recovery", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	// Phase 1: Establish steady state (30 seconds).
	// Send requests at ~2 rps, verify all succeed.
	steadyTotal, steadyErrors := runSteadyPhase(cfg, 30*time.Second)

	steadyRate := 0.0
	if steadyTotal > 0 {
		steadyRate = float64(steadyErrors) / float64(steadyTotal) * 100
	}
	check(&sr, "chaos-recovery:steady-state", steadyRate < 1.0,
		fmt.Sprintf("errors=%d/%d (%.1f%%)", steadyErrors, steadyTotal, steadyRate), 0)

	// Phase 2: Inject failure (send to invalid endpoint).
	// This tests that the proxy handles errors gracefully.
	resp, _, err := authPost(cfg.BaseURL, "/v1/chat/completions", cfg.Token,
		strings.NewReader(`{"model":"NONEXISTENT_MODEL","messages":[{"role":"user","content":"test"}]}`))
	if resp != nil {
		drainBody(resp)
	}

	failureStatus := 0
	if resp != nil {
		failureStatus = resp.StatusCode
	}
	failureHandled := err == nil && resp != nil && (resp.StatusCode == 502 || resp.StatusCode == 503)
	check(&sr, "chaos-recovery:failure-handled-gracefully", failureHandled,
		fmt.Sprintf("got status %d (expected 502 or 503)", failureStatus), 0)

	// Phase 3: Verify recovery (30 seconds).
	// Send normal requests, verify the system is still healthy.
	recoveryTotal, recoveryErrors := runSteadyPhase(cfg, 30*time.Second)

	recoveryRate := 0.0
	if recoveryTotal > 0 {
		recoveryRate = float64(recoveryErrors) / float64(recoveryTotal) * 100
	}
	check(&sr, "chaos-recovery:recovered", recoveryRate < 1.0,
		fmt.Sprintf("post-failure errors=%d/%d (%.1f%%)", recoveryErrors, recoveryTotal, recoveryRate), 0)

	sr.Extra["steady_state_error_rate"] = steadyRate
	sr.Extra["recovery_error_rate"] = recoveryRate

	return sr
}
