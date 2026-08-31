//go:build architecture

package architecture

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ClaimResult records the outcome of a single architectural claim test.
type ClaimResult struct {
	ID       string // A01, A02, etc.
	Category string // reconciliation, routing, tenant, lifecycle, autoscaling, compliance, events
	Method   string // TDD, BDD, EDD, CDD
	Claim    string // human-readable claim
	Passed   bool
}

var (
	matrixMu      sync.Mutex
	matrixResults []ClaimResult
)

// claim registers an architectural claim for the current test. The result
// (pass/fail) is captured automatically when the test completes.
func claim(t *testing.T, id, category, method, description string) {
	t.Helper()
	t.Cleanup(func() {
		matrixMu.Lock()
		defer matrixMu.Unlock()
		matrixResults = append(matrixResults, ClaimResult{
			ID:       id,
			Category: category,
			Method:   method,
			Claim:    description,
			Passed:   !t.Failed(),
		})
	})
}

// PrintMatrix renders the proof matrix as a formatted table to stdout.
func PrintMatrix(results []ClaimResult) {
	width := 105
	fmt.Println()
	fmt.Println(strings.Repeat("=", width))
	fmt.Println("  ARCHITECTURAL PROOF MATRIX — fleet-llm-d")
	fmt.Println(strings.Repeat("=", width))
	fmt.Printf("  %-5s  %-16s  %-4s  %-56s  %s\n", "ID", "CATEGORY", "TYPE", "CLAIM", "RESULT")
	fmt.Println("  " + strings.Repeat("-", width-2))

	passed, failed := 0, 0
	currentCategory := ""
	for _, r := range results {
		if r.Category != currentCategory {
			if currentCategory != "" {
				fmt.Println("  " + strings.Repeat("-", width-2))
			}
			currentCategory = r.Category
		}
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		fmt.Printf("  %-5s  %-16s  %-4s  %-56s  %s\n", r.ID, r.Category, r.Method, r.Claim, status)
	}

	fmt.Println(strings.Repeat("=", width))
	total := passed + failed
	fmt.Printf("  Total: %d  |  Passed: %d  |  Failed: %d  |  Coverage: %.0f%%\n",
		total, passed, failed, float64(passed)/float64(total)*100)
	fmt.Println(strings.Repeat("=", width))
}
