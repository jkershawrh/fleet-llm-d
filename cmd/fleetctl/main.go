package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

const usage = `fleetctl - CLI for fleet-llm-d

Usage:
  fleetctl [global flags] <command> <subcommand> [flags]

Global Flags:
  --server   Fleet controller URL (default: http://localhost:8080)
  --format   Output format: json or table (default: json)

Available Commands:
  clusters    Manage cluster registrations and status
  pools       Manage fleet inference pools
  tenants     Manage tenant configurations and quotas
  rollouts    Manage model rollouts and deployments
  metrics     View fleet metrics
  verify      Verify ledger chains
  matrix      Display test matrix status
`

// printJSON marshals v as indented JSON and prints it to stdout.
func printJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// printTable prints a table with the given headers and rows.
func printTable(headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	// Compute column widths.
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header.
	for i, h := range headers {
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Printf("%-*s", widths[i], h)
	}
	fmt.Println()

	// Print separator.
	for i, w := range widths {
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Print(strings.Repeat("-", w))
	}
	fmt.Println()

	// Print rows.
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if i > 0 {
				fmt.Print("  ")
			}
			fmt.Printf("%-*s", widths[i], cell)
		}
		fmt.Println()
	}
}

// colorStatus returns an ANSI-colored status string for the test matrix.
func colorStatus(status string) string {
	switch status {
	case "red":
		return "\033[31mred\033[0m"
	case "amber":
		return "\033[33mamber\033[0m"
	case "green":
		return "\033[32mgreen\033[0m"
	default:
		return status
	}
}

func main() {
	// Parse global flags manually before the subcommand.
	serverURL := "http://localhost:8080"
	outputFormat := "json"

	// Scan os.Args for global flags before the command.
	args := os.Args[1:]
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 < len(args) {
				serverURL = args[i+1]
				i++
			}
		case "--format":
			if i+1 < len(args) {
				outputFormat = args[i+1]
				i++
			}
		case "--help", "-h":
			fmt.Print(usage)
			os.Exit(0)
		default:
			// Check for --server=value and --format=value forms.
			if strings.HasPrefix(args[i], "--server=") {
				serverURL = strings.TrimPrefix(args[i], "--server=")
			} else if strings.HasPrefix(args[i], "--format=") {
				outputFormat = strings.TrimPrefix(args[i], "--format=")
			} else {
				cmdArgs = append(cmdArgs, args[i])
			}
		}
	}

	if len(cmdArgs) == 0 {
		fmt.Print(usage)
		os.Exit(1)
	}

	client := NewFleetClient(serverURL)
	ctx := context.Background()
	isTable := outputFormat == "table"

	command := cmdArgs[0]
	subArgs := cmdArgs[1:]

	switch command {
	case "clusters":
		runClusters(ctx, client, subArgs, isTable)
	case "pools":
		runPools(ctx, client, subArgs, isTable)
	case "tenants":
		runTenants(ctx, client, subArgs, isTable)
	case "rollouts":
		runRollouts(ctx, client, subArgs, isTable)
	case "metrics":
		runMetrics(ctx, client, subArgs, isTable)
	case "verify":
		runVerify(ctx, client, subArgs, isTable)
	case "matrix":
		runMatrix(subArgs, isTable)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
		fmt.Print(usage)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// clusters
// ----------------------------------------------------------------------------

func runClusters(ctx context.Context, client *FleetClient, args []string, isTable bool) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fleetctl clusters <list|register|deregister> [flags]")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		clusters, err := client.ListClusters(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"ID", "NAME", "REGION", "UTILIZATION"}
			var rows [][]string
			for _, c := range clusters {
				rows = append(rows, []string{
					c.ID, c.Name, c.Region,
					fmt.Sprintf("%.1f%%", c.Utilization*100),
				})
			}
			printTable(headers, rows)
		} else {
			printJSON(clusters)
		}

	case "register":
		fs := flag.NewFlagSet("clusters register", flag.ExitOnError)
		name := fs.String("name", "", "Cluster name (required)")
		region := fs.String("region", "", "Cluster region")
		id := fs.String("id", "", "Cluster ID (auto-generated if empty)")
		_ = fs.Parse(args[1:])

		if *name == "" {
			fmt.Fprintln(os.Stderr, "error: --name is required")
			os.Exit(1)
		}
		reg := ClusterRegistration{
			ID:     *id,
			Name:   *name,
			Region: *region,
		}
		if err := client.RegisterCluster(ctx, reg); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("cluster %q registered\n", *name)

	case "deregister":
		fs := flag.NewFlagSet("clusters deregister", flag.ExitOnError)
		id := fs.String("id", "", "Cluster ID (required)")
		_ = fs.Parse(args[1:])

		if *id == "" {
			fmt.Fprintln(os.Stderr, "error: --id is required")
			os.Exit(1)
		}
		if err := client.DeregisterCluster(ctx, *id); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("cluster %q deregistered\n", *id)

	default:
		fmt.Fprintf(os.Stderr, "unknown clusters subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// pools
// ----------------------------------------------------------------------------

func runPools(ctx context.Context, client *FleetClient, args []string, isTable bool) {
	if len(args) == 0 || args[0] == "list" {
		pools, err := client.ListPools(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"ID", "NAME", "MODEL", "STATUS"}
			var rows [][]string
			for _, p := range pools {
				rows = append(rows, []string{p.ID, p.Name, p.ModelName, p.Status})
			}
			printTable(headers, rows)
		} else {
			printJSON(pools)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "unknown pools subcommand: %s\n", args[0])
	os.Exit(1)
}

// ----------------------------------------------------------------------------
// tenants
// ----------------------------------------------------------------------------

func runTenants(ctx context.Context, client *FleetClient, args []string, isTable bool) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fleetctl tenants <list|usage> [flags]")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		tenants, err := client.ListTenants(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"ID", "NAME", "PRIORITY"}
			var rows [][]string
			for _, t := range tenants {
				rows = append(rows, []string{
					t.ID, t.Name, fmt.Sprintf("%d", t.Priority),
				})
			}
			printTable(headers, rows)
		} else {
			printJSON(tenants)
		}

	case "usage":
		fs := flag.NewFlagSet("tenants usage", flag.ExitOnError)
		id := fs.String("id", "", "Tenant ID (required)")
		_ = fs.Parse(args[1:])

		if *id == "" {
			fmt.Fprintln(os.Stderr, "error: --id is required")
			os.Exit(1)
		}
		usage, err := client.GetTenantUsage(ctx, *id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"TENANT", "TOKENS", "COST", "REQUESTS", "AVG LATENCY (ms)"}
			rows := [][]string{{
				usage.TenantID,
				fmt.Sprintf("%d", usage.TokensConsumed),
				usage.TotalCost,
				fmt.Sprintf("%d", usage.RequestCount),
				fmt.Sprintf("%d", usage.AvgLatencyMs),
			}}
			printTable(headers, rows)
		} else {
			printJSON(usage)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown tenants subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// rollouts
// ----------------------------------------------------------------------------

func runRollouts(ctx context.Context, client *FleetClient, args []string, isTable bool) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fleetctl rollouts <list|promote|rollback> [flags]")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		rollouts, err := client.ListRollouts(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"ID", "POOL", "VERSION", "STATUS", "WEIGHT"}
			var rows [][]string
			for _, r := range rollouts {
				rows = append(rows, []string{
					r.ID, r.PoolID, r.ModelVersion,
					r.Status, fmt.Sprintf("%d%%", r.CurrentWeight),
				})
			}
			printTable(headers, rows)
		} else {
			printJSON(rollouts)
		}

	case "promote":
		fs := flag.NewFlagSet("rollouts promote", flag.ExitOnError)
		id := fs.String("id", "", "Rollout ID (required)")
		_ = fs.Parse(args[1:])

		if *id == "" {
			fmt.Fprintln(os.Stderr, "error: --id is required")
			os.Exit(1)
		}
		state, err := client.PromoteRollout(ctx, *id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"ID", "PHASE", "WEIGHT"}
			rows := [][]string{{state.ID, state.Phase, fmt.Sprintf("%d%%", state.CurrentWeight)}}
			printTable(headers, rows)
		} else {
			printJSON(state)
		}

	case "rollback":
		fs := flag.NewFlagSet("rollouts rollback", flag.ExitOnError)
		id := fs.String("id", "", "Rollout ID (required)")
		_ = fs.Parse(args[1:])

		if *id == "" {
			fmt.Fprintln(os.Stderr, "error: --id is required")
			os.Exit(1)
		}
		state, err := client.RollbackRollout(ctx, *id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"ID", "PHASE", "WEIGHT"}
			rows := [][]string{{state.ID, state.Phase, fmt.Sprintf("%d%%", state.CurrentWeight)}}
			printTable(headers, rows)
		} else {
			printJSON(state)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown rollouts subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// metrics
// ----------------------------------------------------------------------------

func runMetrics(ctx context.Context, client *FleetClient, args []string, isTable bool) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fleetctl metrics <fleet|model> [flags]")
		os.Exit(1)
	}

	switch args[0] {
	case "fleet":
		m, err := client.GetFleetMetrics(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"TOTAL GPUS", "ACTIVE MODELS", "THROUGHPUT", "AVG TTFT (ms)"}
			rows := [][]string{{
				fmt.Sprintf("%d", m.TotalGPUs),
				fmt.Sprintf("%d", m.ActiveModels),
				fmt.Sprintf("%.2f", m.TotalThroughput),
				fmt.Sprintf("%.2f", m.AvgTTFTMs),
			}}
			printTable(headers, rows)
		} else {
			printJSON(m)
		}

	case "model":
		fs := flag.NewFlagSet("metrics model", flag.ExitOnError)
		name := fs.String("name", "", "Model name (required)")
		_ = fs.Parse(args[1:])

		if *name == "" {
			fmt.Fprintln(os.Stderr, "error: --name is required")
			os.Exit(1)
		}
		m, err := client.GetModelMetrics(ctx, *name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if isTable {
			headers := []string{"MODEL", "CLUSTERS", "THROUGHPUT", "P50 (ms)", "P95 (ms)", "P99 (ms)", "CACHE HIT"}
			rows := [][]string{{
				m.Model,
				fmt.Sprintf("%d", len(m.Clusters)),
				fmt.Sprintf("%.2f", m.Throughput),
				fmt.Sprintf("%.2f", m.TTFTP50Ms),
				fmt.Sprintf("%.2f", m.TTFTP95Ms),
				fmt.Sprintf("%.2f", m.TTFTP99Ms),
				fmt.Sprintf("%.1f%%", m.CacheHitRate*100),
			}}
			printTable(headers, rows)
		} else {
			printJSON(m)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown metrics subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// verify
// ----------------------------------------------------------------------------

func runVerify(ctx context.Context, client *FleetClient, args []string, isTable bool) {
	if len(args) == 0 || args[0] != "chains" {
		fmt.Fprintln(os.Stderr, "usage: fleetctl verify chains")
		os.Exit(1)
	}

	results, err := client.VerifyChains(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if isTable {
		headers := []string{"CHAIN", "VALID", "ENTRIES CHECKED", "VERIFIED AT"}
		var rows [][]string
		for chain, v := range results {
			rows = append(rows, []string{
				chain,
				fmt.Sprintf("%v", v.Valid),
				fmt.Sprintf("%d", v.EntriesChecked),
				v.VerifiedAt,
			})
		}
		printTable(headers, rows)
	} else {
		printJSON(results)
	}
}

// ----------------------------------------------------------------------------
// matrix
// ----------------------------------------------------------------------------

// matrixEntry holds a parsed test matrix cell.
type matrixEntry struct {
	capability string
	testType   string
	status     string
	tests      string
	passing    string
	coverage   string
}

// parseMatrixYAML reads matrix.yaml with a simple line-based parser.
// This avoids pulling in a YAML dependency for a well-structured file.
func parseMatrixYAML(path string) ([]matrixEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []matrixEntry
	var currentCapability string
	var currentTestType string
	var currentEntry matrixEntry
	inCapabilities := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip comments and empty lines.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Detect sections.
		if trimmed == "capabilities:" {
			inCapabilities = true
			continue
		}
		if !inCapabilities {
			continue
		}
		// Stop at summary section.
		if trimmed == "summary:" {
			break
		}

		// Count leading spaces to determine nesting level.
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if indent == 2 && strings.HasSuffix(trimmed, ":") {
			// Capability name (indent=2).
			// Save previous entry before switching capabilities.
			if currentTestType != "" {
				entries = append(entries, currentEntry)
			}
			currentCapability = strings.TrimSuffix(trimmed, ":")
			currentTestType = ""
		} else if indent == 4 && strings.HasSuffix(trimmed, ":") {
			// Test type (indent=4).
			// Save previous entry if we had one.
			if currentTestType != "" {
				entries = append(entries, currentEntry)
			}
			currentTestType = strings.TrimSuffix(trimmed, ":")
			currentEntry = matrixEntry{
				capability: currentCapability,
				testType:   currentTestType,
			}
		} else if indent == 6 && strings.Contains(trimmed, ":") {
			// Property (indent=6).
			parts := strings.SplitN(trimmed, ":", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			switch key {
			case "status":
				currentEntry.status = value
			case "tests":
				currentEntry.tests = value
			case "passing":
				currentEntry.passing = value
			case "coverage":
				currentEntry.coverage = value
			}
		}
	}
	// Don't forget the last entry.
	if currentTestType != "" {
		entries = append(entries, currentEntry)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func runMatrix(args []string, isTable bool) {
	// Try default path, then allow override.
	matrixPath := "test/matrix/matrix.yaml"
	if len(args) > 0 {
		matrixPath = args[0]
	}

	entries, err := parseMatrixYAML(matrixPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading matrix: %v\n", err)
		os.Exit(1)
	}

	if isTable {
		headers := []string{"CAPABILITY", "TYPE", "STATUS", "TESTS", "PASSING", "COVERAGE"}
		var rows [][]string
		for _, e := range entries {
			rows = append(rows, []string{
				e.capability, e.testType, colorStatus(e.status),
				e.tests, e.passing, e.coverage,
			})
		}
		printTable(headers, rows)
	} else {
		// For JSON output, build a structured representation.
		type jsonEntry struct {
			Capability string `json:"capability"`
			TestType   string `json:"test_type"`
			Status     string `json:"status"`
			Tests      string `json:"tests"`
			Passing    string `json:"passing"`
			Coverage   string `json:"coverage"`
		}
		var jEntries []jsonEntry
		for _, e := range entries {
			jEntries = append(jEntries, jsonEntry{
				Capability: e.capability,
				TestType:   e.testType,
				Status:     e.status,
				Tests:      e.tests,
				Passing:    e.passing,
				Coverage:   e.coverage,
			})
		}
		printJSON(jEntries)
	}
}
