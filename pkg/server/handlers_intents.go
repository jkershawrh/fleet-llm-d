package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/intents"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

// handleIntent is the v1 compatibility adapter. Non-terminal work is reported
// as deferred; "executed" is reserved for a verified SUCCEEDED operation.
func (fc *FleetController) handleIntent(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var intent intents.FleetIntent
	if err := json.NewDecoder(r.Body).Decode(&intent); err != nil {
		writeError(w, http.StatusBadRequest, "invalid intent JSON: "+err.Error())
		return
	}

	submission, err := fc.submitIntent(r.Context(), intent)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := intents.IntentResponse{IntentID: submission.IntentID, Reason: submission.Reason}
	switch submission.State {
	case intents.StateSucceeded:
		resp.Status = intents.StatusExecuted
	case intents.StateRejected, intents.StateExpired:
		resp.Status = intents.StatusRefused
	default:
		resp.Status = intents.StatusDeferred
	}
	if operation, getErr := fc.IntentService.GetOperation(r.Context(), submission.OperationID); getErr == nil {
		resp.LedgerEntryID = operation.LedgerEntryID
	}
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", "</api/v2/intents>; rel=successor-version")
	writeJSON(w, http.StatusOK, resp)
}

func (fc *FleetController) submitIntent(ctx context.Context, intent intents.FleetIntent) (intents.SubmissionResponse, error) {
	submission, err := fc.IntentService.Submit(ctx, intent)
	if err != nil {
		return intents.SubmissionResponse{}, err
	}
	if fc.FleetRecorder == nil {
		return submission, nil
	}
	persistedIntent, err := fc.IntentService.GetIntent(ctx, submission.IntentID)
	if err != nil {
		return submission, nil
	}
	operation, err := fc.IntentService.GetOperation(ctx, submission.OperationID)
	if err != nil {
		return submission, nil
	}
	intentBytes, err := json.Marshal(persistedIntent)
	if err != nil {
		return submission, nil
	}
	intentDigest := sha256.Sum256(intentBytes)
	actor := "fleet-api"
	if persistedIntent.Proposer != nil && strings.TrimSpace(persistedIntent.Proposer.Subject) != "" {
		actor = persistedIntent.Proposer.Subject
	}
	admissionReason := submission.Reason
	if len(operation.Transitions) > 1 {
		admissionReason = operation.Transitions[1].Reason
	}
	evidence := struct {
		SchemaVersion         string                 `json:"schema_version"`
		IntentID              string                 `json:"intent_id"`
		OperationID           string                 `json:"operation_id"`
		CorrelationID         string                 `json:"correlation_id"`
		IdempotencyKey        string                 `json:"idempotency_key"`
		IntentSpecDigest      string                 `json:"intent_spec_digest"`
		DecisionPackageRef    string                 `json:"decision_package_ref,omitempty"`
		DecisionPackageDigest string                 `json:"decision_package_digest,omitempty"`
		Actor                 string                 `json:"actor"`
		IntentType            intents.IntentType     `json:"intent_type"`
		Pool                  string                 `json:"pool,omitempty"`
		Model                 string                 `json:"model,omitempty"`
		TargetClusters        []string               `json:"target_clusters,omitempty"`
		DesiredReplicas       int                    `json:"desired_replicas,omitempty"`
		AdmissionState        intents.OperationState `json:"admission_state"`
		AdmissionReason       string                 `json:"admission_reason"`
		ExpiresAt             *time.Time             `json:"expires_at,omitempty"`
	}{
		SchemaVersion: "fleet.llm-d.ai/intent-admission/v1", IntentID: persistedIntent.ID,
		OperationID: operation.ID, CorrelationID: operation.CorrelationID,
		IdempotencyKey: persistedIntent.IdempotencyKey, IntentSpecDigest: fmt.Sprintf("%x", intentDigest[:]),
		DecisionPackageRef: persistedIntent.DecisionPackageRef, DecisionPackageDigest: persistedIntent.DecisionPackageDigest,
		Actor: actor, IntentType: persistedIntent.Type, Pool: persistedIntent.Pool, Model: persistedIntent.Model,
		TargetClusters: persistedIntent.TargetClusters, DesiredReplicas: persistedIntent.DesiredReplicas,
		AdmissionState: operation.State, AdmissionReason: admissionReason, ExpiresAt: persistedIntent.ExpiresAt,
	}
	content, err := json.Marshal(evidence)
	if err != nil {
		return submission, nil
	}
	entryType := "fleet.intent." + string(persistedIntent.Type)
	digest := sha256.Sum256(content)
	payloadHash := fmt.Sprintf("%x", digest[:])
	receipt, err := fc.FleetRecorder.RecordProof(ctx, ledger.FleetDecision{
		Type:           entryType,
		CorrelationID:  submission.CorrelationID,
		Content:        content,
		ContentType:    "application/json",
		IdempotencyKey: "fleet:" + operation.ID + ":admission",
		InputHash:      payloadHash,
	})
	if err != nil || receipt == nil {
		// Admission remains durable. Missing audit evidence is retried separately
		// and never stands in for fleet authorization.
		return submission, nil
	}
	if _, attachErr := fc.IntentService.AttachLedgerReceipt(ctx, submission.OperationID, intents.OperationLedgerReceipt{
		EntryID: receipt.EntryID, EntryHash: receipt.EntryHash, EntryType: entryType,
		ChainPosition: receipt.ChainPosition, WrittenTS: receipt.Timestamp.UnixMilli(), InputHash: payloadHash,
		Purpose: intents.ReceiptPurposeAdmission,
	}); attachErr != nil {
		log.Printf("intent %s admitted without attached immutable-ledger proof: %v", submission.IntentID, attachErr)
	}
	return submission, nil
}

func (fc *FleetController) handleSubmitIntentV2(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	var intent intents.FleetIntent
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "invalid Content-Type: "+err.Error())
		return
	}
	switch mediaType {
	case intents.GCLDecisionPackageCloudEventContentType:
		if fc.DecisionPackageDecoder == nil {
			writeError(w, http.StatusServiceUnavailable, "GCL DecisionPackage verification is not configured")
			return
		}
		payload, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if readErr != nil {
			writeError(w, http.StatusBadRequest, "read GCL DecisionPackage CloudEvent: "+readErr.Error())
			return
		}
		intent, err = fc.DecisionPackageDecoder.Decode(contentType, payload)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid GCL DecisionPackage CloudEvent: "+err.Error())
			return
		}
	case "application/json":
		if !fc.AllowOperatorJSONIntents {
			writeError(w, http.StatusUnsupportedMediaType, "application/json operator compatibility is disabled; submit a verified GCL DecisionPackage CloudEvent")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&intent); err != nil {
			writeError(w, http.StatusBadRequest, "invalid intent JSON: "+err.Error())
			return
		}
	default:
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/cloudevents+json (or application/json when operator compatibility is explicitly enabled)")
		return
	}
	if err := intents.ValidateGovernedIntent(intent, time.Now().UTC()); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	submission, err := fc.submitIntent(r.Context(), intent)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	status := http.StatusAccepted
	if submission.State == intents.StateRejected || submission.State == intents.StateExpired {
		status = http.StatusUnprocessableEntity
	}
	w.Header().Set("Location", submission.StatusURL)
	writeJSON(w, status, submission)
}

func (fc *FleetController) handleGetIntentV2(w http.ResponseWriter, r *http.Request) {
	intent, err := fc.IntentService.GetIntent(r.Context(), r.PathValue("id"))
	if errors.Is(err, intents.ErrNotFound) {
		writeError(w, http.StatusNotFound, "intent not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func (fc *FleetController) handleGetOperationV2(w http.ResponseWriter, r *http.Request) {
	operation, err := fc.IntentService.GetOperation(r.Context(), r.PathValue("id"))
	if errors.Is(err, intents.ErrNotFound) {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (fc *FleetController) handleApproveOperationV2(w http.ResponseWriter, r *http.Request) {
	operation, err := fc.IntentService.Approve(r.Context(), r.PathValue("id"), RequestActor(r))
	if errors.Is(err, intents.ErrNotFound) {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	if errors.Is(err, intents.ErrInvalidTransition) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (fc *FleetController) handleCancelOperationV2(w http.ResponseWriter, r *http.Request) {
	operation, err := fc.IntentService.Cancel(r.Context(), r.PathValue("id"), RequestActor(r))
	if errors.Is(err, intents.ErrNotFound) {
		writeError(w, http.StatusNotFound, "operation not found")
		return
	}
	if errors.Is(err, intents.ErrInvalidTransition) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, operation)
}
