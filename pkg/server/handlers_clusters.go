package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
)

// clusterRegistrationRequest is the JSON body for POST /api/v1/clusters.
type clusterRegistrationRequest struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels"`
}

func cloneClusterRecord(record *postgres.ClusterRecord) postgres.ClusterRecord {
	copyRecord := *record
	copyRecord.Labels = make(map[string]string, len(record.Labels))
	for key, value := range record.Labels {
		copyRecord.Labels[key] = value
	}
	return copyRecord
}

// registerCluster applies the durable mutation and its required ledger
// evidence as one fail-closed operation for every API transport.
func (fc *FleetController) registerCluster(ctx context.Context, reg client.ClusterRegistration) error {
	if err := fc.ClusterClient.RegisterCluster(ctx, reg); err != nil {
		return err
	}
	clustersGauge.Add(1)
	if fc.FleetRecorder != nil {
		if _, err := fc.FleetRecorder.RecordClusterRegistration(ctx, reg.ID, reg.Name, reg.Region); err != nil && fc.LedgerGatewayURL != "" {
			if rollbackErr := fc.ClusterClient.DeregisterCluster(ctx, reg.ID); rollbackErr != nil {
				return fmt.Errorf("ledger write failed and cluster compensation failed: %v: %w", rollbackErr, err)
			}
			clustersGauge.Add(-1)
			return fmt.Errorf("ledger write failed (fail-closed): %w", err)
		}
	}
	_ = fc.EventPublisher.Publish(ctx, events.FleetEvent{
		Type: events.EventClusterRegistered, Source: "urn:fleet-llm-d:controller",
		Subject: reg.ID, Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{"name": reg.Name, "region": reg.Region},
	})
	return nil
}

// handleListClusters returns all registered clusters.
func (fc *FleetController) handleListClusters(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
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
	defer ObserveRequest(time.Now())
	var req clusterRegistrationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 253 {
		writeError(w, http.StatusBadRequest, "name exceeds maximum length (253)")
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
	if err := fc.registerCluster(r.Context(), reg); err != nil {
		if errors.Is(err, postgres.ErrClusterAlreadyExists) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			errorsTotal.Inc()
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered", "id": reg.ID})
}

// handleDeregisterCluster removes a cluster by ID.
func (fc *FleetController) handleDeregisterCluster(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	record, err := fc.ClusterRepo.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	original := cloneClusterRecord(record)
	if err := fc.ClusterClient.DeregisterCluster(r.Context(), id); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	clustersGauge.Add(-1)
	if fc.FleetRecorder != nil {
		if _, err := fc.FleetRecorder.RecordClusterDeregistration(r.Context(), id, "operator-requested"); err != nil && fc.LedgerGatewayURL != "" {
			errorsTotal.Inc()
			if rollbackErr := fc.ClusterRepo.Create(r.Context(), original); rollbackErr != nil {
				writeError(w, http.StatusInternalServerError, "ledger write failed and cluster compensation failed: "+rollbackErr.Error()+": "+err.Error())
				return
			}
			clustersGauge.Add(1)
			writeError(w, http.StatusInternalServerError, "ledger write failed (fail-closed): "+err.Error())
			return
		}
	}
	_ = fc.EventPublisher.Publish(r.Context(), events.FleetEvent{
		Type: events.EventClusterDeregistered, Source: "urn:fleet-llm-d:controller",
		Subject: id, Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{"reason": "operator-requested"},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deregistered", "id": id})
}
