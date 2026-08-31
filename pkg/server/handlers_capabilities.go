package server

import (
	"net/http"
	"time"
)

type capabilityStatus struct {
	Model            string                     `json:"model"`
	Status           string                     `json:"status"`
	HealthyProviders []string                   `json:"healthy_providers"`
	RequiredForHA    int                        `json:"required_for_ha"`
	Providers        []providerCapabilityStatus `json:"providers,omitempty"`
}

type providerCapabilityStatus struct {
	ClusterID       string    `json:"cluster_id"`
	Status          string    `json:"status"`
	RoutingEndpoint string    `json:"routing_endpoint,omitempty"`
	MetricsEndpoint string    `json:"metrics_endpoint,omitempty"`
	Freshness       time.Time `json:"freshness,omitempty"`
	PhysicalModel   string    `json:"physical_model"`
	FailureDomain   string    `json:"failure_domain,omitempty"`
}

func (fc *FleetController) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	health := fc.BuildInferenceClusterHealth(r.Context())
	healthy := make(map[string]bool, len(health))
	for _, site := range health {
		healthy[site.ClusterID] = site.Healthy
	}
	cpuModel, gpuModel := fc.cpuPhysicalModel(), fc.gpuPhysicalModel()
	cpuProviders, gpuProviders := fc.providersForModel(cpuModel), fc.providersForModel(gpuModel)
	cpu := capabilityFor(cpuModel, cpuProviders, healthy)
	gpu := capabilityFor(gpuModel, gpuProviders, healthy)
	cpu.Providers = fc.providerCapabilityDetails(r, cpuModel, cpuProviders, healthy)
	gpu.Providers = fc.providerCapabilityDetails(r, gpuModel, gpuProviders, healthy)
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": []capabilityStatus{cpu, gpu}})
}

func (fc *FleetController) providerCapabilityDetails(r *http.Request, model string, providerIDs []string, healthy map[string]bool) []providerCapabilityStatus {
	result := make([]providerCapabilityStatus, 0, len(providerIDs))
	for _, id := range providerIDs {
		record, err := fc.ClusterRepo.Get(r.Context(), id)
		if err != nil {
			continue
		}
		status := "unavailable"
		if healthy[id] {
			status = "healthy"
		}
		if record.Status == "draining" || record.Labels["fleet.llm-d.ai/draining"] == "true" {
			status = "draining"
		}
		failureDomain := record.Region
		if zone := record.Labels["topology.kubernetes.io/zone"]; zone != "" {
			failureDomain = zone
		}
		result = append(result, providerCapabilityStatus{
			ClusterID: id, Status: status, PhysicalModel: model, FailureDomain: failureDomain,
			RoutingEndpoint: record.Labels["fleet.llm-d.ai/egress-address"],
			MetricsEndpoint: record.Labels["fleet.llm-d.ai/metrics-endpoint"], Freshness: record.UpdatedAt,
		})
	}
	return result
}

func capabilityFor(model string, providers []string, healthy map[string]bool) capabilityStatus {
	result := capabilityStatus{Model: model, RequiredForHA: 2}
	for _, provider := range providers {
		if healthy[provider] {
			result.HealthyProviders = append(result.HealthyProviders, provider)
		}
	}
	switch len(result.HealthyProviders) {
	case 0:
		result.Status = "unavailable"
	case 1:
		result.Status = "degraded/non-HA"
	default:
		result.Status = "healthy"
	}
	return result
}
