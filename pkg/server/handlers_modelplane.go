package server

import (
	"fmt"
	"net/http"

	"github.com/llm-d/fleet-llm-d/pkg/cost"
	"github.com/llm-d/fleet-llm-d/pkg/modelplane"
)

// handleModelPlaneClusters returns the most recently watched ModelPlane clusters.
func (fc *FleetController) handleModelPlaneClusters(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.Inc()
	if fc.ModelPlaneWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "ModelPlane integration not configured")
		return
	}
	writeJSON(w, http.StatusOK, fc.ModelPlaneWatcher.LastClusters())
}

// handleModelPlaneDeployments returns the most recently watched ModelPlane deployments.
func (fc *FleetController) handleModelPlaneDeployments(w http.ResponseWriter, _ *http.Request) {
	requestsTotal.Inc()
	if fc.ModelPlaneWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "ModelPlane integration not configured")
		return
	}
	writeJSON(w, http.StatusOK, fc.ModelPlaneWatcher.LastDeployments())
}

// handleModelPlaneDeploymentCost returns the hourly cost of a ModelPlane deployment.
func (fc *FleetController) handleModelPlaneDeploymentCost(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	if fc.ModelPlaneWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "ModelPlane integration not configured")
		return
	}

	deploymentName := r.PathValue("deployment")
	if deploymentName == "" {
		writeError(w, http.StatusBadRequest, "deployment name is required")
		return
	}

	deployments := fc.ModelPlaneWatcher.LastDeployments()
	clusters := fc.ModelPlaneWatcher.LastClusters()

	var target *modelplane.ModelDeployment
	for i := range deployments {
		if deployments[i].Name == deploymentName {
			target = &deployments[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("deployment %q not found", deploymentName))
		return
	}

	hourlyCost, err := cost.ComputeDeploymentCost(*target, clusters, fc.PricingTable)
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deployment":  target.Name,
		"model":       target.Model,
		"replicas":    target.Replicas,
		"hourly_cost": hourlyCost,
	})
}
