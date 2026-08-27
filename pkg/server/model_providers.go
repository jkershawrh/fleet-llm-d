package server

import (
	"context"
	"sort"
)

func (fc *FleetController) providersForModel(model string) []string {
	providers := fc.ModelProviderClusters[model]
	result := append([]string(nil), providers...)
	sort.Strings(result)
	return result
}

func (fc *FleetController) providerServesModel(provider, model string) bool {
	for _, candidate := range fc.ModelProviderClusters[model] {
		if candidate == provider {
			return true
		}
	}
	return false
}

func (fc *FleetController) nextHealthyProvider(ctx context.Context, model string) string {
	allowed := make(map[string]bool, len(fc.ModelProviderClusters[model]))
	for _, provider := range fc.ModelProviderClusters[model] {
		allowed[provider] = true
	}
	providers := make([]string, 0, len(allowed))
	for _, cluster := range fc.BuildInferenceClusterHealth(ctx) {
		if allowed[cluster.ClusterID] && cluster.Healthy {
			providers = append(providers, cluster.ClusterID)
		}
	}
	if len(providers) == 0 {
		return ""
	}
	sort.Strings(providers)
	index := (fc.providerRouteCounter.Add(1) - 1) % uint64(len(providers))
	return providers[index]
}
