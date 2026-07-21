package cost

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
)

// InferenceClassToPricing maps a ModelPlane InferenceClass to fleet GPUPricing.
// It looks up the on-demand pricing tier by default.
func InferenceClassToPricing(ic modelplane.InferenceClass, table *PricingTable) (*GPUPricing, error) {
	hourly, err := table.CostPerHour(ic.GPUType, "on-demand")
	if err != nil {
		return nil, fmt.Errorf("looking up pricing for %s: %w", ic.GPUType, err)
	}
	return &GPUPricing{
		GPUType:     ic.GPUType,
		CostPerHour: hourly,
		MemoryGB:    ic.Memory,
		PricingTier: "on-demand",
	}, nil
}

// ComputeDeploymentCost calculates the total hourly cost of a ModelDeployment
// based on the GPU types used in the clusters where it is deployed.
func ComputeDeploymentCost(md modelplane.ModelDeployment, clusters []modelplane.InferenceCluster, table *PricingTable) (float64, error) {
	if md.Replicas == 0 {
		return 0, nil
	}

	// Build a map of cluster name -> cluster for quick lookup
	clusterMap := make(map[string]modelplane.InferenceCluster, len(clusters))
	for _, c := range clusters {
		clusterMap[c.Name] = c
	}

	// If the deployment has cluster assignments in status, use those.
	// Otherwise, distribute replicas evenly across all provided clusters.
	targetClusters := md.Status.Clusters
	if len(targetClusters) == 0 {
		for _, c := range clusters {
			targetClusters = append(targetClusters, c.Name)
		}
	}

	if len(targetClusters) == 0 {
		return 0, fmt.Errorf("no clusters available for deployment %s", md.Name)
	}

	// Filter to only clusters that exist, have pools, and have a priceable GPU type
	var priceableClusters []string
	for _, name := range targetClusters {
		cluster, ok := clusterMap[name]
		if !ok {
			slog.Info("WARNING: cluster %q in deployment %s not found — replicas will be redistributed", name, md.Name)
			continue
		}
		if len(cluster.Pools) == 0 {
			slog.Warn("cluster has no GPU pools, replicas will be redistributed", "cluster", name)
			continue
		}
		gpuType := cluster.Pools[0].GPUType
		if _, err := table.CostPerHour(gpuType, "on-demand"); err != nil {
			slog.Info("WARNING: GPU type %q on cluster %q has no pricing — replicas will be redistributed", gpuType, name)
			continue
		}
		priceableClusters = append(priceableClusters, name)
	}

	if len(priceableClusters) == 0 {
		return 0, fmt.Errorf("no priceable clusters for deployment %s", md.Name)
	}

	// Compute cost: replicas * cost-per-GPU-hour
	// Use the first available GPU type from the first cluster's pools
	totalCost := 0.0
	replicasPerCluster := md.Replicas / len(priceableClusters)
	remainder := md.Replicas % len(priceableClusters)

	for i, clusterName := range priceableClusters {
		cluster := clusterMap[clusterName]

		replicas := replicasPerCluster
		if i < remainder {
			replicas++
		}

		// Use the first pool's GPU type for pricing
		gpuType := cluster.Pools[0].GPUType
		hourly, _ := table.CostPerHour(gpuType, "on-demand")

		totalCost += float64(replicas) * hourly
	}

	return totalCost, nil
}

// PlacementSuggestion recommends a cluster for cheaper deployment.
type PlacementSuggestion struct {
	Cluster     string  `json:"cluster"`
	GPUType     string  `json:"gpuType"`
	CostPerHour float64 `json:"costPerHour"`
	Savings     float64 `json:"savings"` // vs current placement
}

// OptimizePlacement suggests cheaper clusters for a deployment.
// It returns suggestions sorted by cost ascending (cheapest first).
func OptimizePlacement(md modelplane.ModelDeployment, clusters []modelplane.InferenceCluster, table *PricingTable) []PlacementSuggestion {
	// Calculate current cost for savings comparison
	currentCost, err := ComputeDeploymentCost(md, clusters, table)
	if err != nil {
		currentCost = 0
	}

	var suggestions []PlacementSuggestion

	for _, cluster := range clusters {
		for _, pool := range cluster.Pools {
			if pool.Available <= 0 {
				continue
			}

			hourly, err := table.CostPerHour(pool.GPUType, "on-demand")
			if err != nil {
				continue
			}

			totalCost := hourly * float64(md.Replicas)
			savings := currentCost - totalCost

			suggestions = append(suggestions, PlacementSuggestion{
				Cluster:     cluster.Name,
				GPUType:     pool.GPUType,
				CostPerHour: totalCost,
				Savings:     savings,
			})
		}
	}

	// Sort by cost ascending (cheapest first)
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].CostPerHour < suggestions[j].CostPerHour
	})

	return suggestions
}
