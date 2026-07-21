package metrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// PlatformMetrics aggregates metrics from all four platform systems.
type PlatformMetrics struct {
	Timestamp      string                  `json:"timestamp"`
	Inference      *InferenceMetrics       `json:"inference"`
	Classification *ClassificationMetrics  `json:"classification,omitempty"`
	Governance     *GovernanceMetrics      `json:"governance,omitempty"`
	Fleet          *FleetOperationsMetrics `json:"fleet"`
	Ledger         *LedgerMetrics          `json:"ledger,omitempty"`
}

type InferenceMetrics struct {
	Models        map[string]ModelMetricsDetail `json:"models"`
	TotalRequests int64                         `json:"total_requests"`
	ErrorRate     float64                       `json:"error_rate"`
}

type ModelMetricsDetail struct {
	Replicas   int     `json:"replicas"`
	LatencyP50 float64 `json:"latency_p50_ms"`
	LatencyP95 float64 `json:"latency_p95_ms"`
	Throughput float64 `json:"throughput_rps"`
	Status     string  `json:"status"`
}

type ClassificationMetrics struct {
	TotalClassifications int                     `json:"total_classifications"`
	Agents               map[string]AgentMetrics `json:"agents"`
	TopClasses           []string                `json:"top_classes"`
}

type AgentMetrics struct {
	Count         int     `json:"count"`
	AvgConfidence float64 `json:"avg_confidence"`
}

type GovernanceMetrics struct {
	TotalCycles        int            `json:"total_cycles"`
	Committed          int            `json:"committed"`
	Rejected           int            `json:"rejected"`
	RejectionReasons   map[string]int `json:"rejection_reasons"`
	ActionDistribution map[string]int `json:"action_distribution"`
}

type FleetOperationsMetrics struct {
	Clusters int             `json:"clusters"`
	Pools    int             `json:"pools"`
	Tenants  int             `json:"tenants"`
	Routing  *RoutingMetrics `json:"routing,omitempty"`
}

type RoutingMetrics struct {
	SemanticTiers map[string]float64 `json:"semantic_tiers"`
}

type LedgerMetrics struct {
	TotalEntries int            `json:"total_entries"`
	GCLEntries   int            `json:"gcl_entries"`
	ChainsValid  bool           `json:"chains_valid"`
	Sources      map[string]int `json:"sources"`
}

// PlatformCollector aggregates metrics from all platform systems.
type PlatformCollector struct {
	GCLURL       string
	DeepfieldURL string
	LedgerURL    string
	LedgerToken  string
	ClustersFunc func() int
	PoolsFunc    func() int
	TenantsFunc  func() int
	ProxyStats   func() (map[string]ModelMetricsDetail, int64)
}

// Collect gathers metrics from all systems in parallel.
func (pc *PlatformCollector) Collect(ctx context.Context) *PlatformMetrics {
	result := &PlatformMetrics{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Fleet: &FleetOperationsMetrics{
			Clusters: safeCallInt(pc.ClustersFunc),
			Pools:    safeCallInt(pc.PoolsFunc),
			Tenants:  safeCallInt(pc.TenantsFunc),
		},
	}

	var wg sync.WaitGroup
	client := &http.Client{Timeout: 5 * time.Second}

	// Inference metrics from proxy stats
	if pc.ProxyStats != nil {
		models, total := pc.ProxyStats()
		result.Inference = &InferenceMetrics{
			Models:        models,
			TotalRequests: total,
			ErrorRate:     0.0,
		}
	}

	// GCL governance metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		gov := pc.collectGovernance(ctx, client)
		if gov != nil {
			result.Governance = gov
		}
	}()

	// deepfield-fleet classification metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		cls := pc.collectClassification(ctx, client)
		if cls != nil {
			result.Classification = cls
		}
	}()

	// Standalone immutable-ledger metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		ldg := pc.collectLedger(ctx, client)
		if ldg != nil {
			result.Ledger = ldg
		}
	}()

	wg.Wait()
	return result
}

func (pc *PlatformCollector) collectGovernance(ctx context.Context, client *http.Client) *GovernanceMetrics {
	if pc.GCLURL == "" {
		return nil
	}
	resp, err := client.Get(pc.GCLURL + "/api/v1/cycles")
	if err != nil {
		slog.Warn("platform metrics: GCL unreachable", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}

	var cycles []struct {
		Committed            bool    `json:"committed"`
		ActionType           *string `json:"action_type"`
		FalsificationVerdict *string `json:"falsification_verdict"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cycles); err != nil {
		return nil
	}

	gov := &GovernanceMetrics{
		TotalCycles:        len(cycles),
		ActionDistribution: make(map[string]int),
		RejectionReasons:   make(map[string]int),
	}
	for _, c := range cycles {
		if c.Committed {
			gov.Committed++
		} else {
			gov.Rejected++
		}
		action := "none"
		if c.ActionType != nil {
			action = *c.ActionType
		}
		gov.ActionDistribution[action]++
	}
	return gov
}

func (pc *PlatformCollector) collectClassification(ctx context.Context, client *http.Client) *ClassificationMetrics {
	if pc.DeepfieldURL == "" {
		return nil
	}
	resp, err := client.Get(pc.DeepfieldURL + "/api/v1/classification/records")
	if err != nil {
		slog.Warn("platform metrics: deepfield-fleet unreachable", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}

	var records []struct {
		AgentTier  string  `json:"agent_tier"`
		ClassName  string  `json:"class_name"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil
	}

	cls := &ClassificationMetrics{
		TotalClassifications: len(records),
		Agents:               make(map[string]AgentMetrics),
	}

	tierCounts := make(map[string]int)
	tierConfSum := make(map[string]float64)
	classCounts := make(map[string]int)

	for _, r := range records {
		tierCounts[r.AgentTier]++
		tierConfSum[r.AgentTier] += r.Confidence
		classCounts[r.ClassName]++
	}
	for tier, count := range tierCounts {
		cls.Agents[tier] = AgentMetrics{
			Count:         count,
			AvgConfidence: tierConfSum[tier] / float64(count),
		}
	}

	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range classCounts {
		sorted = append(sorted, kv{k, v})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for i, s := range sorted {
		if i >= 5 {
			break
		}
		cls.TopClasses = append(cls.TopClasses, s.k)
	}
	return cls
}

func (pc *PlatformCollector) collectLedger(ctx context.Context, client *http.Client) *LedgerMetrics {
	if pc.LedgerURL == "" {
		return nil
	}

	ldg := &LedgerMetrics{}

	resp, err := authenticatedGet(ctx, client, pc.LedgerURL+"/api/summary", pc.LedgerToken)
	if err != nil {
		slog.Warn("platform metrics: ledger unreachable", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		var summary struct {
			TotalEntries int            `json:"total_entries"`
			Sources      map[string]int `json:"sources"`
		}
		if json.NewDecoder(resp.Body).Decode(&summary) == nil {
			ldg.TotalEntries = summary.TotalEntries
			ldg.Sources = summary.Sources
			ldg.GCLEntries = summary.Sources["gcl"]
		}
	}

	resp2, err := authenticatedGet(ctx, client, pc.LedgerURL+"/api/verify", pc.LedgerToken)
	if err == nil {
		defer resp2.Body.Close()
		if resp2.StatusCode == 200 {
			var verify struct {
				AllValid bool `json:"all_valid"`
			}
			if json.NewDecoder(resp2.Body).Decode(&verify) == nil {
				ldg.ChainsValid = verify.AllValid
			}
		}
	}
	return ldg
}

func authenticatedGet(ctx context.Context, client *http.Client, target, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return client.Do(req)
}

func safeCallInt(f func() int) int {
	if f == nil {
		return 0
	}
	return f()
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// HandlePlatformMetrics is the HTTP handler for GET /api/v1/metrics/platform.
func HandlePlatformMetrics(collector *PlatformCollector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result := collector.Collect(ctx)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(result)
	}
}
