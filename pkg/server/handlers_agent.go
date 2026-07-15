package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/autoscaling/collector"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

type agentStatusReport struct {
	ClusterID    string `json:"cluster_id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	Phase        string `json:"phase"`
	GPUAvailable int    `json:"gpu_available"`
	GPUTotal     int    `json:"gpu_total"`
	Healthy      bool   `json:"healthy"`
	HealthURL    string `json:"health_url"`
}

type agentMetricsReport struct {
	ClusterID      string  `json:"cluster_id"`
	ThroughputTPS  float64 `json:"throughput_tps"`
	TTFTP50MS      float64 `json:"ttft_p50_ms"`
	TTFTP99MS      float64 `json:"ttft_p99_ms"`
	QueueDepth     int     `json:"queue_depth"`
	GPUUtilization float64 `json:"gpu_utilization"`
	KVCacheHitRate float64 `json:"kv_cache_hit_rate"`
}

type agentEventReport struct {
	ClusterID string          `json:"cluster_id"`
	Event     json.RawMessage `json:"event"`
}

func (fc *FleetController) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var report agentStatusReport
	if err := decodeAgentReport(w, r, &report); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(report.ClusterID) == "" {
		writeError(w, http.StatusBadRequest, "cluster_id is required")
		return
	}
	if report.Name == "" {
		report.Name = report.ClusterID
	}
	if report.GPUAvailable < 0 || report.GPUTotal < 0 || report.GPUAvailable > report.GPUTotal {
		writeError(w, http.StatusBadRequest, "GPU capacity must satisfy 0 <= gpu_available <= gpu_total")
		return
	}

	status := report.Phase
	if !report.Healthy {
		status = "Unhealthy"
	}
	labels := map[string]string{}
	if report.HealthURL != "" {
		labels["health_url"] = report.HealthURL
	}

	record, err := fc.ClusterRepo.Get(r.Context(), report.ClusterID)
	if err == nil {
		record.Name = report.Name
		record.Region = report.Region
		record.GPUAvailable = report.GPUAvailable
		record.GPUTotal = report.GPUTotal
		record.Status = status
		if record.Labels == nil {
			record.Labels = make(map[string]string)
		}
		for k, v := range labels {
			record.Labels[k] = v
		}
		if report.HealthURL == "" {
			delete(record.Labels, "health_url")
		}
		if err := fc.ClusterRepo.Update(r.Context(), *record); err != nil {
			errorsTotal.Add(1)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "accepted", "created": false})
		return
	}

	// Cluster not found: attempt to create.
	newRecord := postgres.ClusterRecord{
		ID:           report.ClusterID,
		Name:         report.Name,
		Region:       report.Region,
		GPUAvailable: report.GPUAvailable,
		GPUTotal:     report.GPUTotal,
		Status:       status,
		Labels:       labels,
		RegisteredAt: time.Now().UTC(),
	}
	if err := fc.ClusterRepo.Create(r.Context(), newRecord); err != nil {
		// Handle duplicate: another request created it concurrently.
		if strings.Contains(err.Error(), "already exists") {
			writeJSON(w, http.StatusConflict, map[string]interface{}{"status": "conflict", "detail": "cluster already registered"})
			return
		}
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clustersGauge.Add(1)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"status": "accepted", "created": true})
}

func (fc *FleetController) handleAgentMetrics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var report agentMetricsReport
	if err := decodeAgentReport(w, r, &report); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(report.ClusterID) == "" {
		writeError(w, http.StatusBadRequest, "cluster_id is required")
		return
	}
	if report.QueueDepth < 0 || report.ThroughputTPS < 0 || report.TTFTP50MS < 0 || report.TTFTP99MS < 0 || report.GPUUtilization < 0 || report.KVCacheHitRate < 0 || report.KVCacheHitRate > 1 {
		writeError(w, http.StatusBadRequest, "metrics must be non-negative and kv_cache_hit_rate must be between 0 and 1")
		return
	}

	fc.MetricsCollector.Add(collector.ClusterMetrics{
		ClusterID: report.ClusterID,
		Pools: []collector.PoolMetrics{{
			PoolName:       "agent-aggregate",
			QueueDepth:     report.QueueDepth,
			TTFT_P50_Ms:    report.TTFTP50MS,
			TTFT_P99_Ms:    report.TTFTP99MS,
			Throughput_TPS: report.ThroughputTPS,
			GPUUtilization: report.GPUUtilization,
			KVCacheHitRate: report.KVCacheHitRate,
		}},
		Timestamp: time.Now().UTC(),
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (fc *FleetController) handleAgentEvent(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var report agentEventReport
	if err := decodeAgentReport(w, r, &report); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(report.ClusterID) == "" || len(report.Event) == 0 || string(report.Event) == "null" {
		writeError(w, http.StatusBadRequest, "cluster_id and event are required")
		return
	}
	var payload interface{}
	if err := json.Unmarshal(report.Event, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "event must be valid JSON")
		return
	}
	if err := fc.EventPublisher.Publish(r.Context(), events.FleetEvent{
		Type:      "fleet.agent.event",
		Source:    "urn:fleet-llm-d:agent:" + report.ClusterID,
		Subject:   report.ClusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func decodeAgentReport(w http.ResponseWriter, r *http.Request, target interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
