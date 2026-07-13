package v1beta1

import (
	"encoding/json"
	"time"
)

type FleetCluster struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta         `json:"metadata,omitempty"`
	Spec     FleetClusterSpec   `json:"spec"`
	Status   FleetClusterStatus `json:"status,omitempty"`
}
type FleetClusterSpec struct {
	ClusterID              string                  `json:"clusterId"`
	TrustDomain            string                  `json:"trustDomain"`
	InfrastructureProvider InfrastructureProvider  `json:"infrastructureProvider,omitempty"`
	ProviderRef            ProviderReference       `json:"providerRef"`
	Capabilities           []string                `json:"capabilities,omitempty"`
	Endpoints              []ClusterEndpoint       `json:"endpoints,omitempty"`
	HeartbeatTTL           string                  `json:"heartbeatTtl,omitempty"`
	AuthorizationRef       *AuthorizationReference `json:"authorizationRef,omitempty"`
}
type ClusterEndpoint struct {
	Type        string                `json:"type"`
	URL         string                `json:"url"`
	CABundleRef *LocalObjectReference `json:"caBundleRef,omitempty"`
}
type ClusterCapacity struct {
	GPUType       string `json:"gpuType,omitempty"`
	TotalGPUs     int    `json:"totalGpus,omitempty"`
	AvailableGPUs int    `json:"availableGpus,omitempty"`
	MemoryBytes   int64  `json:"memoryBytes,omitempty"`
}
type FleetClusterStatus struct {
	CommonStatus
	Connected     bool              `json:"connected,omitempty"`
	Capacity      []ClusterCapacity `json:"capacity,omitempty"`
	LastHeartbeat *time.Time        `json:"lastHeartbeat,omitempty"`
}

type FleetIntent struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta        `json:"metadata,omitempty"`
	Spec     FleetIntentSpec   `json:"spec"`
	Status   FleetIntentStatus `json:"status,omitempty"`
}
type DecisionPackageReference struct {
	URI       string `json:"uri"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature,omitempty"`
}
type ProposerAuthority struct {
	Subject        string `json:"subject"`
	AttestationRef string `json:"attestationRef"`
	Ceiling        string `json:"ceiling"`
}
type IntentApproval struct {
	Subject     string             `json:"subject"`
	Role        string             `json:"role"`
	ApprovedAt  time.Time          `json:"approvedAt"`
	EvidenceRef *EvidenceReference `json:"evidenceRef,omitempty"`
}
type FleetIntentSpec struct {
	ActionClass        string                    `json:"actionClass"`
	TargetRef          NamespacedObjectReference `json:"targetRef"`
	DecisionPackageRef DecisionPackageReference  `json:"decisionPackageRef"`
	Proposer           ProposerAuthority         `json:"proposer"`
	Evidence           []EvidenceReference       `json:"evidence,omitempty"`
	ExpiresAt          time.Time                 `json:"expiresAt"`
	Approvals          []IntentApproval          `json:"approvals,omitempty"`
	CorrelationID      string                    `json:"correlationId"`
	IdempotencyKey     string                    `json:"idempotencyKey"`
	Parameters         IntentParameters          `json:"parameters,omitempty"`
	AuthorizationRef   *AuthorizationReference   `json:"authorizationRef,omitempty"`
}

// IntentParameters preserves provider- and action-specific REST inputs without
// promoting them into the stable fleet API. Values may be any valid JSON; the
// validation helper and CRD schema bound the envelope size and property count.
type IntentParameters map[string]json.RawMessage
type FleetIntentStatus struct {
	CommonStatus
	Phase         OperationPhase         `json:"phase,omitempty"`
	OperationRefs []LocalObjectReference `json:"operationRefs,omitempty"`
	AcceptedAt    *time.Time             `json:"acceptedAt,omitempty"`
}

type FleetOperation struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta           `json:"metadata,omitempty"`
	Spec     FleetOperationSpec   `json:"spec"`
	Status   FleetOperationStatus `json:"status,omitempty"`
}
type FleetOperationSpec struct {
	IntentRef        LocalObjectReference      `json:"intentRef"`
	ActionClass      string                    `json:"actionClass"`
	TargetRef        NamespacedObjectReference `json:"targetRef"`
	PlanDigest       *string                   `json:"planDigest,omitempty"`
	Provider         *ProviderReference        `json:"provider,omitempty"`
	AuthorizationRef *AuthorizationReference   `json:"authorizationRef,omitempty"`
	IdempotencyKey   string                    `json:"idempotencyKey"`
	Compensates      *LocalObjectReference     `json:"compensates,omitempty"`
}

type OperationPhase string

const (
	OperationReceived          OperationPhase = "RECEIVED"
	OperationAccepted          OperationPhase = "ACCEPTED"
	OperationPlanned           OperationPhase = "PLANNED"
	OperationGovernancePending OperationPhase = "GOVERNANCE_PENDING"
	OperationAuthorized        OperationPhase = "AUTHORIZED"
	OperationActuating         OperationPhase = "ACTUATING"
	OperationObserving         OperationPhase = "OBSERVING"
	OperationVerified          OperationPhase = "VERIFIED"
	OperationEvidencePending   OperationPhase = "EVIDENCE_PENDING"
	OperationSucceeded         OperationPhase = "SUCCEEDED"
	OperationRejected          OperationPhase = "REJECTED"
	OperationDeferred          OperationPhase = "DEFERRED"
	OperationExpired           OperationPhase = "EXPIRED"
	OperationFailed            OperationPhase = "FAILED"
	OperationRollingBack       OperationPhase = "ROLLING_BACK"
	OperationRolledBack        OperationPhase = "ROLLED_BACK"
	OperationQuarantined       OperationPhase = "QUARANTINED"
)

type OperationTransition struct {
	Sequence      int64               `json:"sequence"`
	Phase         OperationPhase      `json:"phase"`
	At            time.Time           `json:"at"`
	Reason        string              `json:"reason"`
	Message       string              `json:"message,omitempty"`
	Evidence      []EvidenceReference `json:"evidence,omitempty"`
	LedgerEntryID string              `json:"ledgerEntryId,omitempty"`
}
type LedgerReceipt struct {
	EntryID       string `json:"entryId"`
	EntryHash     string `json:"entryHash"`
	EntryType     string `json:"entryType"`
	ChainPosition int64  `json:"chainPosition"`
	WrittenTS     int64  `json:"writtenTs"`
	InputHash     string `json:"inputHash"`
	Purpose       string `json:"purpose"`
}
type OperationResult struct {
	ProviderOperationID string              `json:"providerOperationId,omitempty"`
	ObservedDigest      string              `json:"observedDigest,omitempty"`
	Evidence            []EvidenceReference `json:"evidence,omitempty"`
}
type FleetOperationStatus struct {
	CommonStatus
	Phase           OperationPhase        `json:"phase,omitempty"`
	Transitions     []OperationTransition `json:"transitions,omitempty"`
	Result          *OperationResult      `json:"result,omitempty"`
	LedgerReceipts  []LedgerReceipt       `json:"ledgerReceipts,omitempty"`
	CompensationRef *LocalObjectReference `json:"compensationRef,omitempty"`
}
