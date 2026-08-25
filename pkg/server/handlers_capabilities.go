package server

import (
	"net/http"
	"time"
)

type capabilityStatus struct {
	Model            string   `json:"model"`
	Status           string   `json:"status"`
	HealthyProviders []string `json:"healthy_providers"`
	RequiredForHA    int      `json:"required_for_ha"`
}

func (fc *FleetController) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	health := fc.BuildClusterHealth(r.Context())
	healthy := make(map[string]bool, len(health))
	for _, site := range health {
		healthy[site.ClusterID] = site.Healthy
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": []capabilityStatus{
		capabilityFor(defaultCPUModel, []string{"oberon", "arena"}, healthy),
		capabilityFor(defaultGPUModel, []string{"brutus"}, healthy),
	}})
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
