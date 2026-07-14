package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		urlFlag            = flag.String("url", "http://localhost:8080", "Base URL of the fleet-controller API")
		metricsFlag        = flag.String("metrics", "http://localhost:9090", "Metrics endpoint URL")
		tokenFlag          = flag.String("token", "", "Bearer token for authenticated endpoints")
		secretFlag         = flag.String("secret", "", "HMAC secret for generating tokens internally")
		suiteFlag          = flag.String("suite", "smoke", "Test suite(s) to run: smoke|stress|soak|pressure|chaos|chaos-recovery|redteam|latency|throughput|inference|scale|all")
		durationFlag       = flag.Duration("duration", 5*time.Minute, "Duration for soak tests")
		outputFlag         = flag.String("output", "test/harness/results/report.json", "Output path for JSON report")
		inferenceModelFlag  = flag.String("inference-model", "", "Model name for inference tests (skips auto-discovery)")
		inferenceModelsFlag = flag.String("inference-models", "", "Comma-separated models for multi-model/fairness tests")
	)
	flag.Parse()

	cfg := Config{
		BaseURL:         strings.TrimRight(*urlFlag, "/"),
		MetricsURL:      strings.TrimRight(*metricsFlag, "/"),
		Token:           *tokenFlag,
		Secret:          *secretFlag,
		Duration:        *durationFlag,
		Output:          *outputFlag,
		InferenceModel:  *inferenceModelFlag,
		InferenceModels: *inferenceModelsFlag,
	}

	// If --secret is provided but no --token, generate a token internally.
	if cfg.Secret != "" && cfg.Token == "" {
		token, err := generateToken(cfg.Secret, "harness-admin", "admin", 24*time.Hour)
		if err != nil {
			log.Fatalf("Failed to generate token from secret: %v", err)
		}
		cfg.Token = token
		log.Printf("Generated admin token from --secret (valid 24h)")
	}

	suites := parseSuites(*suiteFlag)
	log.Printf("fleet-llm-d test harness")
	log.Printf("Target: %s", cfg.BaseURL)
	log.Printf("Suites: %v", suites)

	var results []SuiteResult

	for _, suite := range suites {
		log.Printf("--- Running suite: %s ---", suite)
		var result SuiteResult

		switch suite {
		case "smoke":
			result = RunSmoke(cfg)
		case "stress":
			result = RunStress(cfg)
		case "soak":
			result = RunSoak(cfg)
		case "pressure":
			result = RunPressure(cfg)
		case "chaos":
			result = RunChaos(cfg)
		case "chaos-recovery":
			result = RunChaosRecovery(cfg)
		case "redteam":
			result = RunRedTeam(cfg)
		case "latency":
			result = RunLatency(cfg)
		case "throughput":
			result = RunThroughput(cfg)
		case "inference":
			result = RunInference(cfg)
		case "multimodel":
			result = RunMultiModel(cfg)
		case "fairness":
			result = RunFairness(cfg)
		case "scale":
			result = RunScale(cfg)
		default:
			log.Printf("Unknown suite: %s (skipping)", suite)
			continue
		}

		log.Printf("--- Suite %s complete: passed=%d failed=%d skipped=%d duration=%s ---",
			result.Name, result.Passed, result.Failed, result.Skipped,
			formatDuration(result.Duration))
		results = append(results, result)
	}

	report := GenerateReport(cfg.BaseURL, results)

	// Print formatted report to stdout.
	PrintReport(report)

	// Write JSON report to file.
	if cfg.Output != "" {
		if err := WriteReport(report, cfg.Output); err != nil {
			log.Printf("Warning: failed to write report: %v", err)
		} else {
			log.Printf("JSON report written to %s", cfg.Output)
		}
	}

	// Exit with non-zero status if any tests failed.
	if report.TotalFailed > 0 {
		os.Exit(1)
	}
}

// parseSuites splits the suite flag into individual suite names.
func parseSuites(s string) []string {
	allSuites := []string{"smoke", "stress", "soak", "pressure", "chaos", "chaos-recovery", "redteam", "latency", "throughput", "inference", "multimodel", "fairness", "scale"}

	if s == "all" {
		return allSuites
	}

	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			valid := false
			for _, a := range allSuites {
				if p == a {
					valid = true
					break
				}
			}
			if valid {
				result = append(result, p)
			} else {
				fmt.Fprintf(os.Stderr, "Warning: unknown suite %q (valid: %s)\n", p, strings.Join(allSuites, ", "))
			}
		}
	}

	if len(result) == 0 {
		return []string{"smoke"}
	}
	return result
}
