package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Report aggregates results from all suites that were run.
type Report struct {
	Timestamp   string        `json:"timestamp"`
	Target      string        `json:"target"`
	TotalPassed int           `json:"total_passed"`
	TotalFailed int           `json:"total_failed"`
	TotalSkipped int          `json:"total_skipped"`
	Duration    time.Duration `json:"duration_ns"`
	Suites      []SuiteResult `json:"suites"`
}

// GenerateReport creates a Report from a slice of suite results.
func GenerateReport(target string, suites []SuiteResult) Report {
	r := Report{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Target:    target,
		Suites:    suites,
	}
	for _, s := range suites {
		r.TotalPassed += s.Passed
		r.TotalFailed += s.Failed
		r.TotalSkipped += s.Skipped
		r.Duration += s.Duration
	}
	return r
}

// WriteReport serializes the report to JSON and writes it to the given path.
func WriteReport(r Report, path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// PrintReport outputs a formatted table of the report to stdout.
func PrintReport(r Report) {
	sep := strings.Repeat("=", 76)
	thin := strings.Repeat("-", 76)

	fmt.Println()
	fmt.Println(sep)
	fmt.Println("  FLEET-LLM-D TEST HARNESS REPORT")
	fmt.Println(sep)
	fmt.Printf("  Target:    %s\n", r.Target)
	fmt.Printf("  Timestamp: %s\n", r.Timestamp)
	fmt.Printf("  Duration:  %s\n", formatDuration(r.Duration))
	fmt.Println(thin)

	for _, s := range r.Suites {
		fmt.Println()
		fmt.Printf("  Suite: %-20s  Duration: %s\n", s.Name, formatDuration(s.Duration))
		fmt.Printf("  Passed: %d  Failed: %d  Skipped: %d\n", s.Passed, s.Failed, s.Skipped)
		fmt.Println(thin)

		for _, c := range s.Checks {
			icon := "PASS"
			if !c.Passed {
				if strings.HasPrefix(c.Detail, "SKIP:") {
					icon = "SKIP"
				} else {
					icon = "FAIL"
				}
			}
			latStr := ""
			if c.Latency > 0 {
				latStr = fmt.Sprintf(" (%dms)", c.Latency)
			}
			fmt.Printf("    [%s] %s%s\n", icon, c.Name, latStr)
			if c.Detail != "" && !c.Passed {
				fmt.Printf("           %s\n", c.Detail)
			}
		}

		if s.Latencies != nil {
			fmt.Println()
			fmt.Printf("    Latency: p50=%.1fms p95=%.1fms p99=%.1fms min=%.1fms max=%.1fms mean=%.1fms\n",
				s.Latencies.P50, s.Latencies.P95, s.Latencies.P99,
				s.Latencies.Min, s.Latencies.Max, s.Latencies.Mean)
		}

		if s.Extra != nil {
			for k, v := range s.Extra {
				fmt.Printf("    %s: %v\n", k, v)
			}
		}
	}

	fmt.Println()
	fmt.Println(sep)
	verdict := "PASS"
	if r.TotalFailed > 0 {
		verdict = "FAIL"
	}
	fmt.Printf("  RESULT: %s  (passed=%d failed=%d skipped=%d)\n",
		verdict, r.TotalPassed, r.TotalFailed, r.TotalSkipped)
	fmt.Println(sep)
	fmt.Println()
}
