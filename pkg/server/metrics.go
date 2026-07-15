package server

import (
	"context"
	"expvar"
	"fmt"
	"net/http"
	"runtime"
)

// Prometheus-style counters tracked via expvar.
var (
	requestsTotal = expvar.NewInt("fleet_requests_total")
	errorsTotal   = expvar.NewInt("fleet_errors_total")
	clustersGauge = expvar.NewInt("fleet_clusters_registered")
	poolsGauge    = expvar.NewInt("fleet_pools_active")
	tenantsGauge  = expvar.NewInt("fleet_tenants_active")
	rolloutsGauge = expvar.NewInt("fleet_rollouts_active")
)

// InitGauges initializes metric gauges from existing persistent data.
func (fc *FleetController) InitGauges(ctx context.Context) {
	if clusters, err := fc.ClusterClient.ListClusters(ctx); err == nil {
		clustersGauge.Add(int64(len(clusters)))
	}
}

// setupMetricsServer creates the metrics HTTP server mux.
func setupMetricsServer() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", handlePrometheusMetrics)
	mux.Handle("GET /debug/vars", expvar.Handler())
	return mux
}

func handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fmt.Fprintf(w, "# HELP fleet_requests_total Total API requests\n")
	fmt.Fprintf(w, "# TYPE fleet_requests_total counter\n")
	fmt.Fprintf(w, "fleet_requests_total %d\n", requestsTotal.Value())

	fmt.Fprintf(w, "# HELP fleet_errors_total Total API errors\n")
	fmt.Fprintf(w, "# TYPE fleet_errors_total counter\n")
	fmt.Fprintf(w, "fleet_errors_total %d\n", errorsTotal.Value())

	fmt.Fprintf(w, "# HELP fleet_clusters_registered Number of registered clusters\n")
	fmt.Fprintf(w, "# TYPE fleet_clusters_registered gauge\n")
	fmt.Fprintf(w, "fleet_clusters_registered %d\n", clustersGauge.Value())

	fmt.Fprintf(w, "# HELP fleet_pools_active Number of active inference pools\n")
	fmt.Fprintf(w, "# TYPE fleet_pools_active gauge\n")
	fmt.Fprintf(w, "fleet_pools_active %d\n", poolsGauge.Value())

	fmt.Fprintf(w, "# HELP fleet_tenants_active Number of active tenants\n")
	fmt.Fprintf(w, "# TYPE fleet_tenants_active gauge\n")
	fmt.Fprintf(w, "fleet_tenants_active %d\n", tenantsGauge.Value())

	fmt.Fprintf(w, "# HELP fleet_rollouts_active Number of active rollouts\n")
	fmt.Fprintf(w, "# TYPE fleet_rollouts_active gauge\n")
	fmt.Fprintf(w, "fleet_rollouts_active %d\n", rolloutsGauge.Value())

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(w, "# HELP fleet_process_memory_bytes Process memory in bytes\n")
	fmt.Fprintf(w, "# TYPE fleet_process_memory_bytes gauge\n")
	fmt.Fprintf(w, "fleet_process_memory_bytes{type=\"alloc\"} %d\n", ms.Alloc)
	fmt.Fprintf(w, "fleet_process_memory_bytes{type=\"sys\"} %d\n", ms.Sys)
	fmt.Fprintf(w, "fleet_process_memory_bytes{type=\"heap_inuse\"} %d\n", ms.HeapInuse)

	fmt.Fprintf(w, "# HELP fleet_go_goroutines Number of goroutines\n")
	fmt.Fprintf(w, "# TYPE fleet_go_goroutines gauge\n")
	fmt.Fprintf(w, "fleet_go_goroutines %d\n", runtime.NumGoroutine())
}
