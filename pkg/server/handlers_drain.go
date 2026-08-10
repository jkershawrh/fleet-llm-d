package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// recordToLedger writes an audit evidence entry to the immutable ledger.
// When the ledger is configured in HTTP mode (LedgerGatewayURL is set), write
// failures return an error so the caller can fail closed -- a configured
// ledger error must never be silently ignored.
func (fc *FleetController) recordToLedger(entryType, clusterID string) error {
	if fc.FleetRecorder == nil {
		return nil
	}
	if _, err := fc.FleetRecorder.RecordPlacement(
		context.Background(), entryType, clusterID, 0, "", entryType,
	); err != nil {
		if fc.LedgerGatewayURL != "" {
			// Fail closed: configured ledger must not be bypassed.
			return fmt.Errorf("ledger write failed (fail-closed): %w", err)
		}
		slog.Warn("failed to record to ledger", "type", entryType, "cluster", clusterID, "error", err)
	}
	return nil
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
	original := cloneClusterRecord(record)

	if record.Status == postgres.ClusterStatusDraining {
		writeError(w, http.StatusConflict, "cluster is already draining")
		return
	}
	if record.Status == postgres.ClusterStatusDrained {
		writeError(w, http.StatusConflict, "cluster is already drained")
		return
	}
	if record.Status != postgres.ClusterStatusRunning && record.Status != postgres.ClusterStatusHealthy && record.Status != postgres.ClusterStatusDegraded {
		writeError(w, http.StatusConflict, "cluster must be Running, Healthy, or Degraded to drain")
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

	if err := fc.recordToLedger("fleet.cluster.drain_started", clusterID); err != nil {
		errorsTotal.Inc()
		if rollbackErr := fc.ClusterRepo.Update(r.Context(), original); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "ledger write failed and drain compensation failed: "+rollbackErr.Error()+": "+err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if fc.SessionTable != nil {
		fc.SessionTable.UnbindCluster(clusterID)
	}

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
	original := cloneClusterRecord(record)

	if record.Status == postgres.ClusterStatusRunning || record.Status == postgres.ClusterStatusHealthy {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"cluster_id": clusterID,
			"status":     record.Status,
		})
		return
	}

	record.Status = postgres.ClusterStatusRunning
	delete(record.Labels, "drain_started_at")
	if record.Labels == nil {
		record.Labels = make(map[string]string)
	}
	record.Labels["activated_at"] = time.Now().UTC().Format(time.RFC3339)

	if err := fc.ClusterRepo.Update(r.Context(), *record); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := fc.recordToLedger("fleet.cluster.activated", clusterID); err != nil {
		errorsTotal.Inc()
		if rollbackErr := fc.ClusterRepo.Update(r.Context(), original); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "ledger write failed and activation compensation failed: "+rollbackErr.Error()+": "+err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster_id": clusterID,
		"status":     "running",
	})
}
