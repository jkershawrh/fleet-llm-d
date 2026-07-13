// Package v1beta1 contains the storage-version API contracts for fleet-llm-d.
//
// The structs intentionally do not depend on a Kubernetes client library. This
// keeps the contracts usable by controllers, admission services, CLIs, and
// conformance providers without forcing a particular Kubernetes runtime.
package v1beta1

import "time"

const (
	GroupName  = "fleet.llm-d.ai"
	Version    = "v1beta1"
	APIVersion = GroupName + "/" + Version
)

// TypeMeta and ObjectMeta contain the stable Kubernetes wire fields used by
// the API contracts. Controllers may translate these to metav1 equivalents.
type TypeMeta struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	CreationTimestamp *time.Time        `json:"creationTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

type LocalObjectReference struct {
	Name string `json:"name"`
}

type NamespacedObjectReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

// AuthorizationReference binds an approved ARE grant to the exact mutation.
// SpecDigest is the SHA-256 digest of the canonical next spec.
type AuthorizationReference struct {
	GrantID        string    `json:"grantId"`
	ActionClass    string    `json:"actionClass"`
	ObjectUID      string    `json:"objectUid,omitempty"`
	SpecDigest     string    `json:"specDigest"`
	Audience       string    `json:"audience"`
	ExpiresAt      time.Time `json:"expiresAt"`
	IdempotencyKey string    `json:"idempotencyKey"`
	BreakGlass     bool      `json:"breakGlass,omitempty"`
	IncidentRef    string    `json:"incidentRef,omitempty"`
}

type ProviderReference struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

type EvidenceReference struct {
	URI       string `json:"uri"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType,omitempty"`
}

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition follows the Kubernetes condition shape and includes the observed
// generation so stale observations are unambiguous.
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
	Reason             string          `json:"reason"`
	Message            string          `json:"message,omitempty"`
}

type FreshnessStatus struct {
	ObservedAt time.Time `json:"observedAt,omitempty"`
	ValidUntil time.Time `json:"validUntil,omitempty"`
	Stale      bool      `json:"stale,omitempty"`
}

// CommonStatus is embedded in every status type. CRD schemas expose these
// fields at the status root, matching Kubernetes status conventions.
type CommonStatus struct {
	ObservedGeneration           int64               `json:"observedGeneration,omitempty"`
	Conditions                   []Condition         `json:"conditions,omitempty"`
	SpecDigest                   string              `json:"specDigest,omitempty"`
	ProviderRefs                 []ProviderReference `json:"providerRefs,omitempty"`
	Freshness                    *FreshnessStatus    `json:"freshness,omitempty"`
	CorrelationID                string              `json:"correlationId,omitempty"`
	LastSuccessfulReconciliation *time.Time          `json:"lastSuccessfulReconciliation,omitempty"`
}

type InfrastructureProvider string

const (
	InfrastructureProviderModelPlane  InfrastructureProvider = "ModelPlane"
	InfrastructureProviderDirectAgent InfrastructureProvider = "DirectAgent"
)

type RoutingProvider string

const (
	RoutingProviderModelPlaneGateway RoutingProvider = "ModelPlaneGateway"
	RoutingProviderFleetGateway      RoutingProvider = "FleetGateway"
)

type KVCacheProvider string

const (
	KVCacheProviderLlmDNative    KVCacheProvider = "LlmDNative"
	KVCacheProviderFleetTransfer KVCacheProvider = "FleetTransfer"
)

type TransferProtocol string

const (
	TransferProtocolGRPC TransferProtocol = "GRPC"
	TransferProtocolNIXL TransferProtocol = "NIXL"
)

type TransferFallbackPolicy string

const (
	TransferFallbackDeny TransferFallbackPolicy = "Deny"
	TransferFallbackGRPC TransferFallbackPolicy = "GRPC"
)
