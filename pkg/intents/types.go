package intents

import (
	"time"
)

type IntentType string

const (
	IntentPreWarm    IntentType = "pre_warm"
	IntentScale      IntentType = "scale"
	IntentShedLoad   IntentType = "shed_load"
	IntentAlert      IntentType = "alert"
	IntentMigrate    IntentType = "migrate"
	IntentRoute      IntentType = "route"
	IntentDeploy     IntentType = "deploy"
	IntentKVTransfer IntentType = "kv_transfer"
	IntentNoAction   IntentType = "no_action"
)

type IntentStatus string

const (
	// StatusAccepted means admission policy passed. It deliberately does not
	// mean the requested side effect has happened.
	StatusAccepted IntentStatus = "accepted"
	StatusExecuted IntentStatus = "executed"
	StatusRefused  IntentStatus = "refused"
	StatusDeferred IntentStatus = "deferred"
)

// OperationState is the externally visible lifecycle of a governed fleet
// action. States are uppercase to match the v1beta1 FleetOperation contract.
type OperationState string

const (
	StateReceived          OperationState = "RECEIVED"
	StateAccepted          OperationState = "ACCEPTED"
	StatePlanned           OperationState = "PLANNED"
	StateGovernancePending OperationState = "GOVERNANCE_PENDING"
	StateAuthorized        OperationState = "AUTHORIZED"
	StateActuating         OperationState = "ACTUATING"
	StateObserving         OperationState = "OBSERVING"
	StateVerified          OperationState = "VERIFIED"
	StateEvidencePending   OperationState = "EVIDENCE_PENDING"
	StateSucceeded         OperationState = "SUCCEEDED"
	StateRejected          OperationState = "REJECTED"
	StateDeferred          OperationState = "DEFERRED"
	StateExpired           OperationState = "EXPIRED"
	StateFailed            OperationState = "FAILED"
	StateRollingBack       OperationState = "ROLLING_BACK"
	StateRolledBack        OperationState = "ROLLED_BACK"
	StateQuarantined       OperationState = "QUARANTINED"
)

// EvidenceDigest binds an intent to immutable evidence without copying the
// evidence payload into the fleet API.
type EvidenceDigest struct {
	URI       string `json:"uri"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
}

// ProposerAuthority is optional, non-authoritative provenance presented by a
// decision producer. Fleet admission and approval policy makes the execution
// decision; a ledger receipt is never an execution credential.
type ProposerAuthority struct {
	Subject      string     `json:"subject"`
	AuthorityRef string     `json:"authority_ref"`
	MaxAction    string     `json:"max_action,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type FleetIntent struct {
	ID                    string                 `json:"id"`
	CorrelationID         string                 `json:"correlation_id,omitempty"`
	IdempotencyKey        string                 `json:"idempotency_key,omitempty"`
	Type                  IntentType             `json:"type"`
	Confidence            float64                `json:"confidence"`
	HorizonSeconds        int                    `json:"horizon_seconds"`
	Justification         string                 `json:"justification"`
	StateSnapshot         map[string]interface{} `json:"state_snapshot"`
	CreatedAt             time.Time              `json:"created_at"`
	ExpiresAt             *time.Time             `json:"expires_at,omitempty"`
	DecisionPackageRef    string                 `json:"decision_package_ref,omitempty"`
	DecisionPackageDigest string                 `json:"decision_package_digest,omitempty"`
	Evidence              []EvidenceDigest       `json:"evidence,omitempty"`
	Proposer              *ProposerAuthority     `json:"proposer,omitempty"`

	// Type-specific fields (flattened for simplicity)
	Model             string   `json:"model,omitempty"`
	Pool              string   `json:"pool,omitempty"`
	TargetReplicas    int      `json:"target_replicas,omitempty"`
	DesiredReplicas   int      `json:"desired_replicas,omitempty"`
	CurrentReplicas   int      `json:"current_replicas,omitempty"`
	TargetClusters    []string `json:"target_clusters,omitempty"`
	MaxInflight       int      `json:"max_inflight,omitempty"`
	DurationSeconds   int      `json:"duration_seconds,omitempty"`
	Metric            string   `json:"metric,omitempty"`
	Severity          string   `json:"severity,omitempty"`
	Message           string   `json:"message,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
}

type IntentResponse struct {
	IntentID      string       `json:"intent_id"`
	Status        IntentStatus `json:"status"`
	Reason        string       `json:"reason"`
	LedgerEntryID string       `json:"ledger_entry_id,omitempty"`
}

// OperationTransition is an append-only, monotonically sequenced state
// change. Downstream CloudEvent IDs are derived from operation ID + sequence.
type OperationTransition struct {
	Sequence      int64          `json:"sequence"`
	State         OperationState `json:"state"`
	At            time.Time      `json:"at"`
	Reason        string         `json:"reason,omitempty"`
	Actor         string         `json:"actor,omitempty"`
	LedgerEntryID string         `json:"ledger_entry_id,omitempty"`
}

type OperationLedgerReceipt struct {
	EntryID       string               `json:"entry_id"`
	EntryHash     string               `json:"entry_hash"`
	EntryType     string               `json:"entry_type"`
	ChainPosition int64                `json:"chain_position"`
	WrittenTS     int64                `json:"written_ts"`
	InputHash     string               `json:"input_hash"`
	Purpose       LedgerReceiptPurpose `json:"purpose"`
}

type LedgerReceiptPurpose string

const (
	ReceiptPurposeAdmission             LedgerReceiptPurpose = "admission"
	ReceiptPurposeAuthorizationDecision LedgerReceiptPurpose = "authorization_decision"
	ReceiptPurposeOutcome               LedgerReceiptPurpose = "outcome"
)

const (
	// AuthorizationAudienceFleetController is the only audience accepted by
	// the fleet operation service for an infrastructure execution grant.
	AuthorizationAudienceFleetController = "fleet-llm-d"

	// LedgerEntryTypeAuthorizationDecision and LedgerEntryTypeOutcome bind a
	// receipt purpose to the producer-owned immutable-ledger entry type. A
	// caller cannot relabel an unrelated proof as execution evidence.
	LedgerEntryTypeAuthorizationDecision = "fleet.operation.authorization_decision"
	LedgerEntryTypeOutcome               = "fleet.operation.verified"
)

// AuthorizationReference is the Fleet-owned execution decision bound to one
// operation plan. Immutable-ledger receipts can prove this decision was
// recorded, but they never substitute for this authorization.
type AuthorizationReference struct {
	GrantID        string    `json:"grant_id"`
	Subject        string    `json:"subject"`
	ActionClass    string    `json:"action_class"`
	ObjectUID      string    `json:"object_uid"`
	SpecDigest     string    `json:"spec_digest"`
	Audience       string    `json:"audience"`
	ExpiresAt      time.Time `json:"expires_at"`
	IdempotencyKey string    `json:"idempotency_key"`
	BreakGlass     bool      `json:"break_glass,omitempty"`
	IncidentRef    string    `json:"incident_ref,omitempty"`
}

// ProviderReference identifies the provider that owns actuation for an
// operation plan. It mirrors the v1beta1 FleetOperation provider reference in
// the REST/domain projection.
type ProviderReference struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

// FleetOperation is the REST projection of the v1beta1 FleetOperation CRD.
type FleetOperation struct {
	ID               string                   `json:"id"`
	ObjectUID        string                   `json:"object_uid,omitempty"`
	IntentID         string                   `json:"intent_id"`
	CorrelationID    string                   `json:"correlation_id"`
	IdempotencyKey   string                   `json:"idempotency_key,omitempty"`
	ActionClass      string                   `json:"action_class"`
	State            OperationState           `json:"state"`
	PlanDigest       string                   `json:"plan_digest,omitempty"`
	Provider         *ProviderReference       `json:"provider,omitempty"`
	ObservedDigest   string                   `json:"observed_digest,omitempty"`
	AuthorizationRef *AuthorizationReference  `json:"authorization_ref,omitempty"`
	LedgerEntryID    string                   `json:"ledger_entry_id,omitempty"`
	LedgerReceipts   []OperationLedgerReceipt `json:"ledger_receipts,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
	Transitions      []OperationTransition    `json:"transitions"`
}

// SubmissionResponse is returned by POST /api/v2/intents.
type SubmissionResponse struct {
	IntentID      string         `json:"intent_id"`
	OperationID   string         `json:"operation_id"`
	CorrelationID string         `json:"correlation_id"`
	State         OperationState `json:"state"`
	Reason        string         `json:"reason,omitempty"`
	StatusURL     string         `json:"status_url"`
}
