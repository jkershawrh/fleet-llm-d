package modelplane

import (
	"strings"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
)

// InferenceClusterToClusterInfo converts a ModelPlane InferenceCluster to a solver.ClusterInfo.
func InferenceClusterToClusterInfo(ic InferenceCluster) solver.ClusterInfo {
	// ID = Name (ModelPlane uses name as identifier)
	// Region, Labels mapped directly
	// GPUCapacity computed from pools: sum Available and Count across pools, collect unique GPU types
	var totalAvailable, totalCount int
	var gpuTypes []string
	seen := map[string]bool{}
	for _, p := range ic.Pools {
		totalAvailable += p.Available
		totalCount += p.Count
		if !seen[p.GPUType] {
			gpuTypes = append(gpuTypes, p.GPUType)
			seen[p.GPUType] = true
		}
	}
	utilization := 0.0
	if totalCount > 0 {
		utilization = 1.0 - float64(totalAvailable)/float64(totalCount)
	}
	return solver.ClusterInfo{
		ID:     ic.Name,
		Name:   ic.Name,
		Region: ic.Region,
		Labels: ic.Labels,
		GPUCapacity: solver.GPUCapacity{
			Available: totalAvailable,
			Total:     totalCount,
			Types:     gpuTypes,
		},
		Utilization: utilization,
	}
}

// ModelEndpointToBackend converts a ModelPlane ModelEndpoint to a routing.Backend.
func ModelEndpointToBackend(me ModelEndpoint) routing.Backend {
	// Infer runtime from model name: if model contains "sglang" -> sglang, "ovms" -> ovms, default "vllm"
	runtime := "vllm"
	lower := strings.ToLower(me.Model)
	if strings.Contains(lower, "sglang") {
		runtime = "sglang"
	} else if strings.Contains(lower, "ovms") {
		runtime = "ovms"
	}
	return routing.Backend{
		Name:    me.Name,
		URL:     me.URL,
		Runtime: runtime,
		Healthy: me.Ready,
	}
}

// ModelDeploymentToFleetPool converts a ModelPlane ModelDeployment to a controller.FleetPoolState.
func ModelDeploymentToFleetPool(md ModelDeployment) controller.FleetPoolState {
	// Map Phase: Running->Running, Scaling->Placing, Error->Failed, default->Pending
	var phase v1alpha1.FleetPhase
	switch md.Status.Phase {
	case "Running":
		phase = v1alpha1.FleetPhaseRunning
	case "Scaling":
		phase = v1alpha1.FleetPhasePlacing
	case "Error":
		phase = v1alpha1.FleetPhaseFailed
	default:
		phase = v1alpha1.FleetPhasePending
	}
	return controller.FleetPoolState{
		Name:            md.Name,
		Model:           md.Model,
		Source:          "modelplane",
		DesiredClusters: md.Status.Clusters,
		Phase:           phase,
		LastReconciled:  time.Now(),
	}
}

// InferenceClassToGPUType returns the GPU type string for cost model lookup.
func InferenceClassToGPUType(ic InferenceClass) string {
	return ic.GPUType
}
