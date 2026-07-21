package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
)

// clusterRegistrationRequest is the JSON body for POST /api/v1/clusters.
type clusterRegistrationRequest struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels"`
}

// handleListClusters returns all registered clusters.
func (fc *FleetController) handleListClusters(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	clusters, err := fc.ClusterClient.ListClusters(r.Context())
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, clusters)
}

// handleRegisterCluster registers a new cluster.
func (fc *FleetController) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	var req clusterRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	reg := client.ClusterRegistration{
		ID:     req.ID,
		Name:   req.Name,
		Region: req.Region,
		Labels: req.Labels,
	}
	reg, err := client.NormalizeClusterRegistration(reg)
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := fc.ClusterClient.RegisterCluster(r.Context(), reg); err != nil {
		errorsTotal.Inc()
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "conflict") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	clustersGauge.Add(1)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered", "id": reg.ID})
}

// handleDeregisterCluster removes a cluster by ID.
func (fc *FleetController) handleDeregisterCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if err := fc.ClusterClient.DeregisterCluster(r.Context(), id); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	clustersGauge.Add(-1)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deregistered", "id": id})
}
