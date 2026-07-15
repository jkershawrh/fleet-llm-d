package server

import (
	"net/http"
)

// handleFleetMetrics returns fleet-wide aggregated metrics.
func (fc *FleetController) handleFleetMetrics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	clusters, err := fc.ClusterClient.ListClusters(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clusterIDs := make([]string, len(clusters))
	for i, c := range clusters {
		clusterIDs[i] = c.ID
	}
	fleetMetrics, err := fc.MetricsFederator.FederateMetrics(r.Context(), clusterIDs)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fleetMetrics)
}

// handleModelMetrics returns metrics for a specific model.
func (fc *FleetController) handleModelMetrics(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	model := r.PathValue("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}
	modelMetrics, err := fc.MetricsFederator.GetModelMetrics(r.Context(), model)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, modelMetrics)
}
