package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "inference-mock"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("inference_throughput_tps 1\ninference_ttft_p50_ms 10\ninference_ttft_p99_ms 20\ninference_queue_depth 0\ngpu_utilization 0\nkv_cache_hit_rate 1\n"))
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object":  "list",
			"cluster": clusterID,
			"data":    []map[string]string{{"id": "e2e-model"}},
		})
	})
	mux.HandleFunc("POST /v1/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "e2e-completion",
			"cluster": clusterID,
			"choices": []map[string]string{{"text": "ok"}},
		})
	})

	log.Printf("inference mock listening on :%s for cluster %s", port, clusterID)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
