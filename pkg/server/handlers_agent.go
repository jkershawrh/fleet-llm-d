package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	InferenceURL string `json:"inference_url"`
}

type agentMetricsReport struct {
	ClusterID      string  `json:"cluster_id"`
	ThroughputTPS  float64 `json:"throughput_tps"`
	TTFTP50MS      float64 `json:"ttft_p50_ms"`
	TTFTP99MS      float64 `json:"ttft_p99_ms"`
	QueueDepth     int     `json:"queue_depth"`
	GPUUtilization float64 `json:"gpu_utilization"`
	KVCacheHitRate float64 `json:"kv_cache_hit_rate"`

	// EPP (Endpoint Picker) signals from llm-d
	PoolSaturation   float64 `json:"pool_saturation"`
	ReadyEndpoints   int     `json:"ready_endpoints"`
	KVCacheUtilization float64 `json:"kv_cache_utilization"`
	InflightRequests int     `json:"inflight_requests"`
}

type agentEventReport struct {
	ClusterID string          `json:"cluster_id"`
	Event     json.RawMessage `json:"event"`
}

func (fc *FleetController) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	var report agentStatusReport
	if err := decodeAgentReport(w, r, &report); err != nil {
		errorsTotal.Inc()
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
	if err := validateAgentURL("health_url", report.HealthURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateAgentURL("inference_url", report.InferenceURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	status := strings.TrimSpace(report.Phase)
	if status == "" {
		if report.Healthy {
			status = "Running"
		} else {
			status = "Unhealthy"
		}
	} else if !report.Healthy && status != "Degraded" && status != "Unhealthy" {
		status = "Unhealthy"
	}
	labels := map[string]string{}
	if report.HealthURL != "" {
		labels["health_url"] = report.HealthURL
	}
	if report.InferenceURL != "" {
		labels["inference_url"] = report.InferenceURL
	}

	record, err := fc.ClusterRepo.Get(r.Context(), report.ClusterID)
	if err == nil {
		applyAgentStatus(record, report, status)
		if err := fc.ClusterRepo.Update(r.Context(), *record); err != nil {
			errorsTotal.Inc()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "accepted", "created": false})
		return
	}
	if !errors.Is(err, postgres.ErrClusterNotFound) {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Cluster not found: reject unregistered agents. Clusters must be
	// pre-registered via POST /api/v1/clusters before agents can report
	// status. This prevents rogue agents from self-declaring identities.
	writeError(w, http.StatusForbidden, fmt.Sprintf("cluster %q is not registered; register via POST /api/v1/clusters first", report.ClusterID))
}

func applyAgentStatus(record *postgres.ClusterRecord, report agentStatusReport, status string) {
	record.Name = report.Name
	record.Region = report.Region
	record.GPUAvailable = report.GPUAvailable
	record.GPUTotal = report.GPUTotal
	if record.Status == postgres.ClusterStatusDraining || record.Status == postgres.ClusterStatusDrained {
		// Never let agent status override an active drain.
	} else if activatedAt, ok := record.Labels["activated_at"]; ok {
		if t, err := time.Parse(time.RFC3339, activatedAt); err == nil && time.Since(t) < 30*time.Second {
			// Protect recently-activated clusters from immediate agent downgrade.
		} else {
			record.Status = status
			delete(record.Labels, "activated_at")
		}
	} else {
		record.Status = status
	}
	if record.Labels == nil {
		record.Labels = make(map[string]string)
	}
	for label, value := range map[string]string{
		"health_url":    report.HealthURL,
		"inference_url": report.InferenceURL,
	} {
		if value == "" {
			delete(record.Labels, label)
		} else {
			record.Labels[label] = value
		}
	}
}

func validateAgentURL(field, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP(S) URL", field)
	}
	return nil
}

func (fc *FleetController) handleAgentMetrics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	var report agentMetricsReport
	if err := decodeAgentReport(w, r, &report); err != nil {
		errorsTotal.Inc()
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
			PoolName:           "agent-aggregate",
			QueueDepth:         report.QueueDepth,
			TTFT_P50_Ms:        report.TTFTP50MS,
			TTFT_P99_Ms:        report.TTFTP99MS,
			Throughput_TPS:     report.ThroughputTPS,
			GPUUtilization:     report.GPUUtilization,
			KVCacheHitRate:     report.KVCacheHitRate,
			PoolSaturation:     report.PoolSaturation,
			ReadyEndpoints:     report.ReadyEndpoints,
			KVCacheUtilization: report.KVCacheUtilization,
			InflightRequests:   report.InflightRequests,
		}},
		Timestamp: time.Now().UTC(),
	})
	UpdateAgentMetrics(report.ClusterID, report.ThroughputTPS, report.TTFTP50MS, report.TTFTP99MS,
		float64(report.QueueDepth), report.GPUUtilization, report.KVCacheHitRate,
		report.PoolSaturation, float64(report.ReadyEndpoints), report.KVCacheUtilization, float64(report.InflightRequests))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (fc *FleetController) handleAgentEvent(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	var report agentEventReport
	if err := decodeAgentReport(w, r, &report); err != nil {
		errorsTotal.Inc()
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
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (fc *FleetController) handleAgentPolicies(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	clusterID := r.PathValue("cluster_id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster_id is required")
		return
	}

	type quotaEntry struct {
		MaxRPS             float64 `json:"max_rps"`
		MaxConcurrent      uint64  `json:"max_concurrent"`
		MaxTokensPerMinute uint64  `json:"max_tokens_per_minute"`
	}
	type placementEntry struct {
		AllowedModels []string `json:"allowed_models"`
		DeniedModels  []string `json:"denied_models"`
	}
	type policyResponse struct {
		Quotas    map[string]quotaEntry `json:"quotas"`
		Placement *placementEntry       `json:"placement,omitempty"`
	}

	resp := policyResponse{
		Quotas: make(map[string]quotaEntry),
	}

	tenants, err := fc.TenantRepo.List(r.Context())
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, t := range tenants {
		q := quotaEntry{MaxRPS: 100, MaxConcurrent: 100, MaxTokensPerMinute: 1_000_000}
		if quotas := t.Quotas; quotas != nil {
			if v, ok := quotas["maxTokensPerMinute"]; ok {
				if f, ok := v.(float64); ok {
					q.MaxTokensPerMinute = uint64(f)
				}
			}
			if v, ok := quotas["maxConcurrentRequests"]; ok {
				if f, ok := v.(float64); ok {
					q.MaxConcurrent = uint64(f)
					q.MaxRPS = f * 10
				}
			}
		}
		resp.Quotas[t.ID] = q
	}

	writeJSON(w, http.StatusOK, resp)
}

func decodeAgentReport(w http.ResponseWriter, r *http.Request, target interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
