package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

func (fc *FleetController) recordToLedger(entryType, clusterID string) {
	if fc.FleetRecorder == nil {
		return
	}
	if _, err := fc.FleetRecorder.RecordPlacement(
		context.Background(), entryType, clusterID, 0, "", entryType,
	); err != nil {
		slog.Warn("failed to record to ledger", "type", entryType, "cluster", clusterID, "error", err)
	}
}

func (fc *FleetController) handleDrainCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	clusterID := r.PathValue("id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}

	record, err := fc.ClusterRepo.Get(r.Context(), clusterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	if record.Status == postgres.ClusterStatusDraining {
		writeError(w, http.StatusConflict, "cluster is already draining")
		return
	}
	if record.Status == postgres.ClusterStatusDrained {
		writeError(w, http.StatusConflict, "cluster is already drained")
		return
	}
	if record.Status != postgres.ClusterStatusRunning && record.Status != postgres.ClusterStatusHealthy {
		writeError(w, http.StatusConflict, "cluster must be Running or Healthy to drain")
		return
	}

	record.Status = postgres.ClusterStatusDraining
	if record.Labels == nil {
		record.Labels = make(map[string]string)
	}
	record.Labels["drain_started_at"] = time.Now().UTC().Format(time.RFC3339)

	if err := fc.ClusterRepo.Update(r.Context(), *record); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if fc.SessionTable != nil {
		fc.SessionTable.UnbindCluster(clusterID)
	}

	fc.recordToLedger("fleet.cluster.drain_started", clusterID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster_id": clusterID,
		"status":     "draining",
	})
}

func (fc *FleetController) handleActivateCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	clusterID := r.PathValue("id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}

	record, err := fc.ClusterRepo.Get(r.Context(), clusterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	record.Status = postgres.ClusterStatusRunning
	delete(record.Labels, "drain_started_at")

	if err := fc.ClusterRepo.Update(r.Context(), *record); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fc.recordToLedger("fleet.cluster.activated", clusterID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster_id": clusterID,
		"status":     "running",
	})
}
