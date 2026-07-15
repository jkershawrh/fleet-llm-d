package server

import (
	"encoding/json"
	"net/http"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// rolloutCreateRequest is the JSON body for POST /api/v1/rollouts.
type rolloutCreateRequest struct {
	PoolID       string `json:"pool_id"`
	ModelVersion string `json:"model_version"`
	Strategy     string `json:"strategy"`
}

// handleListRollouts returns all rollouts.
func (fc *FleetController) handleListRollouts(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	rollouts, err := fc.RolloutRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rollouts)
}

// handleCreateRollout creates a new rollout.
func (fc *FleetController) handleCreateRollout(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var req rolloutCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.PoolID == "" || req.ModelVersion == "" {
		writeError(w, http.StatusBadRequest, "pool_id and model_version are required")
		return
	}

	record := postgres.RolloutRecord{
		PoolID:        req.PoolID,
		ModelVersion:  req.ModelVersion,
		Strategy:      map[string]interface{}{"type": req.Strategy},
		Status:        "pending",
		CurrentWeight: 0,
	}
	if err := fc.RolloutRepo.Create(r.Context(), record); err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rolloutsGauge.Add(1)
	writeJSON(w, http.StatusCreated, map[string]string{
		"status":        "created",
		"pool_id":       req.PoolID,
		"model_version": req.ModelVersion,
	})
}

// handlePromoteRollout promotes a canary rollout.
func (fc *FleetController) handlePromoteRollout(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rollout id is required")
		return
	}
	state, err := fc.RolloutController.AdvanceRollout(r.Context(), id)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleRollbackRollout rolls back a rollout.
func (fc *FleetController) handleRollbackRollout(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "rollout id is required")
		return
	}
	state, err := fc.RolloutController.RollbackRollout(r.Context(), id)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}
