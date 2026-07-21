package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// fleet-llm-d metrics registry.
// Uses standard library only (no prometheus/client_golang) per project conventions.
// Emits valid Prometheus text exposition format with histograms and labels.

// --- Counters ---

type counter struct {
	val atomic.Int64
}

func (c *counter) Inc()          { c.val.Add(1) }
func (c *counter) Add(n int64)   { c.val.Add(n) }
func (c *counter) Value() int64  { return c.val.Load() }

type labeledCounter struct {
	mu   sync.RWMutex
	vals map[string]*atomic.Int64
}

func newLabeledCounter() *labeledCounter {
	return &labeledCounter{vals: make(map[string]*atomic.Int64)}
}

func (lc *labeledCounter) Inc(label string) {
	lc.mu.RLock()
	v, ok := lc.vals[label]
	lc.mu.RUnlock()
	if ok {
		v.Add(1)
		return
	}
	lc.mu.Lock()
	if v, ok = lc.vals[label]; ok {
		lc.mu.Unlock()
		v.Add(1)
		return
	}
	v = &atomic.Int64{}
	v.Add(1)
	lc.vals[label] = v
	lc.mu.Unlock()
}

func (lc *labeledCounter) snapshot() map[string]int64 {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	out := make(map[string]int64, len(lc.vals))
	for k, v := range lc.vals {
		out[k] = v.Load()
	}
	return out
}

// --- Gauges ---

type gauge struct {
	val atomic.Int64
}

func (g *gauge) Set(v int64) { g.val.Store(v) }
func (g *gauge) Inc()        { g.val.Add(1) }
func (g *gauge) Dec()        { g.val.Add(-1) }
func (g *gauge) Add(n int64) { g.val.Add(n) }
func (g *gauge) Value() int64 { return g.val.Load() }

type floatGauge struct {
	bits atomic.Uint64
}

func (fg *floatGauge) Set(v float64)  { fg.bits.Store(math.Float64bits(v)) }
func (fg *floatGauge) Value() float64 { return math.Float64frombits(fg.bits.Load()) }

// --- Histogram ---

type histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

func newHistogram(buckets []float64) *histogram {
	sorted := make([]float64, len(buckets))
	copy(sorted, buckets)
	sort.Float64s(sorted)
	return &histogram{
		buckets: sorted,
		counts:  make([]uint64, len(sorted)),
	}
}

func (h *histogram) Observe(v float64) {
	h.mu.Lock()
	h.sum += v
	h.count++
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.mu.Unlock()
}

func (h *histogram) snapshot() (buckets []float64, counts []uint64, sum float64, count uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := make([]float64, len(h.buckets))
	c := make([]uint64, len(h.counts))
	copy(b, h.buckets)
	copy(c, h.counts)
	return b, c, h.sum, h.count
}

// --- Labeled Float Gauge (for per-cluster agent metrics) ---

type labeledFloatGauge struct {
	mu   sync.RWMutex
	vals map[string]float64
}

func newLabeledFloatGauge() *labeledFloatGauge {
	return &labeledFloatGauge{vals: make(map[string]float64)}
}

func (lg *labeledFloatGauge) Set(label string, v float64) {
	lg.mu.Lock()
	lg.vals[label] = v
	lg.mu.Unlock()
}

func (lg *labeledFloatGauge) snapshot() map[string]float64 {
	lg.mu.RLock()
	defer lg.mu.RUnlock()
	out := make(map[string]float64, len(lg.vals))
	for k, v := range lg.vals {
		out[k] = v
	}
	return out
}

// --- Global metrics ---

var (
	// Request counters
	requestsTotal = &counter{}
	errorsTotal   = &counter{}

	// Request latency histogram (seconds)
	requestDuration = newHistogram([]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10})

	// Routing decisions by strategy
	routingDecisions = newLabeledCounter()

	// Placement solver duration (seconds)
	solverDuration = newHistogram([]float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1})

	// Autoscaler actions by type (scale_up, scale_down, migrate)
	autoscalerActions = newLabeledCounter()

	// Resource gauges
	clustersGauge = &gauge{}
	poolsGauge    = &gauge{}
	tenantsGauge  = &gauge{}
	rolloutsGauge = &gauge{}

	// Per-cluster agent-reported metrics (Phase 1c: re-exported as Prometheus)
	agentThroughput    = newLabeledFloatGauge()
	agentTTFTP50       = newLabeledFloatGauge()
	agentTTFTP99       = newLabeledFloatGauge()
	agentQueueDepth    = newLabeledFloatGauge()
	agentGPUUtil       = newLabeledFloatGauge()
	agentKVCacheHitRate = newLabeledFloatGauge()
)

// ObserveRequest records a request's duration. Call with defer.
func ObserveRequest(start time.Time) {
	requestDuration.Observe(time.Since(start).Seconds())
}

// RecordRoutingDecision records a routing decision by strategy name.
func RecordRoutingDecision(strategy string) {
	routingDecisions.Inc(strategy)
}

// ObserveSolverDuration records a placement solver execution duration.
func ObserveSolverDuration(start time.Time) {
	solverDuration.Observe(time.Since(start).Seconds())
}

// RecordAutoscalerAction records an autoscaler action (scale_up, scale_down, migrate).
func RecordAutoscalerAction(action string) {
	autoscalerActions.Inc(action)
}

// UpdateAgentMetrics updates the per-cluster metrics from agent reports.
func UpdateAgentMetrics(clusterID string, throughput, ttftP50, ttftP99, queueDepth, gpuUtil, kvHitRate float64) {
	agentThroughput.Set(clusterID, throughput)
	agentTTFTP50.Set(clusterID, ttftP50)
	agentTTFTP99.Set(clusterID, ttftP99)
	agentQueueDepth.Set(clusterID, queueDepth)
	agentGPUUtil.Set(clusterID, gpuUtil)
	agentKVCacheHitRate.Set(clusterID, kvHitRate)
}

// InitGauges initializes metric gauges from existing persistent data.
func (fc *FleetController) InitGauges(ctx context.Context) {
	if clusters, err := fc.ClusterClient.ListClusters(ctx); err == nil {
		clustersGauge.Set(int64(len(clusters)))
	}
}

// setupMetricsServer creates the metrics HTTP server mux.
func setupMetricsServer() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", handlePrometheusMetrics)
	return mux
}

func handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Request counters
	writeCounter(w, "fleet_requests_total", "Total API requests", requestsTotal.Value())
	writeCounter(w, "fleet_errors_total", "Total API errors", errorsTotal.Value())

	// Request latency histogram
	writeHistogram(w, "fleet_request_duration_seconds", "API request duration in seconds", requestDuration)

	// Routing decisions by strategy
	writeCounterVec(w, "fleet_routing_decisions_total", "Routing decisions by strategy", "strategy", routingDecisions.snapshot())

	// Solver duration histogram
	writeHistogram(w, "fleet_solver_duration_seconds", "Placement solver duration in seconds", solverDuration)

	// Autoscaler actions
	writeCounterVec(w, "fleet_autoscaler_actions_total", "Autoscaler actions by type", "action", autoscalerActions.snapshot())

	// Resource gauges
	writeGauge(w, "fleet_clusters_registered", "Number of registered clusters", clustersGauge.Value())
	writeGauge(w, "fleet_pools_active", "Number of active inference pools", poolsGauge.Value())
	writeGauge(w, "fleet_tenants_active", "Number of active tenants", tenantsGauge.Value())
	writeGauge(w, "fleet_rollouts_active", "Number of active rollouts", rolloutsGauge.Value())

	// Per-cluster agent-reported metrics
	writeGaugeVecFloat(w, "fleet_inference_throughput_tps", "Inference throughput (tokens per second) by cluster", "cluster", agentThroughput.snapshot())
	writeGaugeVecFloat(w, "fleet_inference_ttft_p50_ms", "Time to first token p50 (ms) by cluster", "cluster", agentTTFTP50.snapshot())
	writeGaugeVecFloat(w, "fleet_inference_ttft_p99_ms", "Time to first token p99 (ms) by cluster", "cluster", agentTTFTP99.snapshot())
	writeGaugeVecFloat(w, "fleet_inference_queue_depth", "Inference queue depth by cluster", "cluster", agentQueueDepth.snapshot())
	writeGaugeVecFloat(w, "fleet_gpu_utilization_percent", "GPU/accelerator utilization (0-1) by cluster", "cluster", agentGPUUtil.snapshot())
	writeGaugeVecFloat(w, "fleet_kv_cache_hit_rate", "KV cache hit rate (0-1) by cluster", "cluster", agentKVCacheHitRate.snapshot())

	// Process metrics
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(w, "# HELP fleet_process_memory_bytes Process memory in bytes\n")
	fmt.Fprintf(w, "# TYPE fleet_process_memory_bytes gauge\n")
	fmt.Fprintf(w, "fleet_process_memory_bytes{type=\"alloc\"} %d\n", ms.Alloc)
	fmt.Fprintf(w, "fleet_process_memory_bytes{type=\"sys\"} %d\n", ms.Sys)
	fmt.Fprintf(w, "fleet_process_memory_bytes{type=\"heap_inuse\"} %d\n", ms.HeapInuse)

	writeGauge(w, "fleet_go_goroutines", "Number of goroutines", int64(runtime.NumGoroutine()))
}

// --- Prometheus text format helpers ---

func writeCounter(w http.ResponseWriter, name, help string, val int64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, val)
}

func writeGauge(w http.ResponseWriter, name, help string, val int64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, val)
}

func writeCounterVec(w http.ResponseWriter, name, help, label string, vals map[string]int64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	keys := sortedKeys(vals)
	for _, k := range keys {
		fmt.Fprintf(w, "%s{%s=%q} %d\n", name, label, k, vals[k])
	}
}

func writeGaugeVecFloat(w http.ResponseWriter, name, help, label string, vals map[string]float64) {
	if len(vals) == 0 {
		return
	}
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	keys := sortedKeysFloat(vals)
	for _, k := range keys {
		fmt.Fprintf(w, "%s{%s=%q} %g\n", name, label, k, vals[k])
	}
}

func writeHistogram(w http.ResponseWriter, name, help string, h *histogram) {
	buckets, counts, sum, count := h.snapshot()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	var cumulative uint64
	for i, b := range buckets {
		cumulative += counts[i]
		fmt.Fprintf(w, "%s_bucket{le=\"%g\"} %d\n", name, b, cumulative)
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, count)
	fmt.Fprintf(w, "%s_sum %g\n", name, sum)
	fmt.Fprintf(w, "%s_count %d\n", name, count)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysFloat(m map[string]float64) []string {
	return sortedKeys(m)
}
