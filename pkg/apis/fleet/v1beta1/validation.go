package v1beta1

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

const (
	MaxIntentParameterProperties = 64
	MaxIntentParametersBytes     = 64 * 1024
	FleetAuthorizationAudience   = "fleet-llm-d"
)

// ValidationError identifies a stable JSON field path for admission clients.
type ValidationError struct{ Field, Message string }

func (e ValidationError) Error() string { return e.Field + ": " + e.Message }

func DefaultFleetInferencePool(spec *FleetInferencePoolSpec) {
	if spec.InfrastructureProvider == "" {
		spec.InfrastructureProvider = InfrastructureProviderModelPlane
	}
}
func DefaultFleetRoutingPolicy(spec *FleetRoutingPolicySpec) {
	if spec.Provider == "" {
		spec.Provider = RoutingProviderModelPlaneGateway
	}
}
func DefaultKVCacheTransferPolicy(spec *KVCacheTransferPolicySpec) {
	if spec.Provider == "" {
		spec.Provider = KVCacheProviderLlmDNative
	}
	if spec.Transport.Protocol == "" {
		spec.Transport.Protocol = TransferProtocolGRPC
	}
	if spec.Transport.FallbackPolicy == "" {
		spec.Transport.FallbackPolicy = TransferFallbackDeny
	}
}
func DefaultFleetCluster(spec *FleetClusterSpec) {
	if spec.InfrastructureProvider == "" {
		spec.InfrastructureProvider = InfrastructureProviderModelPlane
	}
}
func ValidateFleetInferencePool(spec FleetInferencePoolSpec) []error {
	var errs []error
	if strings.TrimSpace(spec.Model.Name) == "" {
		errs = append(errs, ValidationError{"spec.model.name", "must not be empty"})
	}
	if strings.TrimSpace(spec.HomeFleet) == "" {
		errs = append(errs, ValidationError{"spec.homeFleet", "must not be empty"})
	}
	if spec.Placement.MinClusters < 0 {
		errs = append(errs, ValidationError{"spec.placement.minClusters", "must be non-negative"})
	}
	if spec.Placement.MaxClusters > 0 && spec.Placement.MaxClusters < spec.Placement.MinClusters {
		errs = append(errs, ValidationError{"spec.placement.maxClusters", "must be greater than or equal to minClusters"})
	}
	if !validInfrastructureProvider(spec.InfrastructureProvider) {
		errs = append(errs, ValidationError{"spec.infrastructureProvider", "must be ModelPlane or DirectAgent"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateFleetRoutingPolicy(spec FleetRoutingPolicySpec) []error {
	var errs []error
	if spec.Provider != RoutingProviderModelPlaneGateway && spec.Provider != RoutingProviderFleetGateway {
		errs = append(errs, ValidationError{"spec.provider", "must be ModelPlaneGateway or FleetGateway"})
	}
	if strings.TrimSpace(spec.Strategy) == "" {
		errs = append(errs, ValidationError{"spec.strategy", "must not be empty"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidatePlacementPolicy(spec PlacementPolicySpec) []error {
	var errs []error
	for i, affinity := range spec.Affinity {
		if strings.TrimSpace(affinity.Type) == "" {
			errs = append(errs, ValidationError{fmt.Sprintf("spec.affinity[%d].type", i), "must not be empty"})
		}
		if affinity.Weight < 0 || affinity.Weight > 100 {
			errs = append(errs, ValidationError{fmt.Sprintf("spec.affinity[%d].weight", i), "must be between 0 and 100"})
		}
	}
	if spec.Spreading != nil && spec.Spreading.MaxSkew < 1 {
		errs = append(errs, ValidationError{"spec.spreading.maxSkew", "must be at least 1"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateTenantProfile(spec TenantProfileSpec) []error {
	var errs []error
	if spec.Quotas.MaxTokensPerMinute < 0 {
		errs = append(errs, ValidationError{"spec.quotas.maxTokensPerMinute", "must be non-negative"})
	}
	if spec.Quotas.MaxConcurrentRequests < 0 || spec.Quotas.MaxModels < 0 || spec.Quotas.GPUBudget.MaxGPUs < 0 {
		errs = append(errs, ValidationError{"spec.quotas", "quota values must be non-negative"})
	}
	if spec.Priority < 0 || spec.Priority > 100 {
		errs = append(errs, ValidationError{"spec.priority", "must be between 0 and 100"})
	}
	if spec.CostControl != nil && (spec.CostControl.AlertThreshold < 0 || spec.CostControl.AlertThreshold > 1) {
		errs = append(errs, ValidationError{"spec.costControl.alertThreshold", "must be between 0 and 1"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateFleetScalingPolicy(spec FleetScalingPolicySpec) []error {
	var errs []error
	if len(spec.Objectives) == 0 {
		errs = append(errs, ValidationError{"spec.objectives", "must contain at least one objective"})
	}
	if strings.TrimSpace(spec.Strategy) == "" {
		errs = append(errs, ValidationError{"spec.strategy", "must not be empty"})
	}
	if spec.Constraints.GlobalMaxGPUs < 0 || spec.Constraints.MaxScaleUpRate < 0 {
		errs = append(errs, ValidationError{"spec.constraints", "limits must be non-negative"})
	}
	if spec.CrossCluster != nil && (spec.CrossCluster.MigrationThreshold < 0 || spec.CrossCluster.MigrationThreshold > 1) {
		errs = append(errs, ValidationError{"spec.crossCluster.migrationThreshold", "must be between 0 and 1"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateModelLifecycle(spec ModelLifecycleSpec) []error {
	var errs []error
	if strings.TrimSpace(spec.Model.Name) == "" || strings.TrimSpace(spec.Model.Version) == "" {
		errs = append(errs, ValidationError{"spec.model", "name and version must not be empty"})
	}
	if strings.TrimSpace(spec.FleetPoolRef) == "" {
		errs = append(errs, ValidationError{"spec.fleetPoolRef", "must not be empty"})
	}
	if strings.TrimSpace(spec.Strategy.Type) == "" {
		errs = append(errs, ValidationError{"spec.strategy.type", "must not be empty"})
	}
	if spec.Strategy.Canary != nil {
		canary := spec.Strategy.Canary
		if canary.InitialWeight < 0 || canary.InitialWeight > 100 || canary.WeightIncrement < 1 || canary.WeightIncrement > 100 {
			errs = append(errs, ValidationError{"spec.strategy.canary", "weights must be within the valid percentage range"})
		}
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateKVCacheTransferPolicy(spec KVCacheTransferPolicySpec) []error {
	var errs []error
	if spec.Provider != KVCacheProviderLlmDNative && spec.Provider != KVCacheProviderFleetTransfer {
		errs = append(errs, ValidationError{"spec.provider", "must be LlmDNative or FleetTransfer"})
	}
	if spec.Transport.Protocol != TransferProtocolGRPC && spec.Transport.Protocol != TransferProtocolNIXL {
		errs = append(errs, ValidationError{"spec.transport.protocol", "must be GRPC or NIXL"})
	}
	if spec.Transport.FallbackPolicy != TransferFallbackDeny && spec.Transport.FallbackPolicy != TransferFallbackGRPC {
		errs = append(errs, ValidationError{"spec.transport.fallbackPolicy", "must be Deny or GRPC"})
	}
	if spec.Transport.Protocol == TransferProtocolNIXL && spec.Provider != KVCacheProviderFleetTransfer {
		errs = append(errs, ValidationError{"spec.transport.protocol", "NIXL requires the FleetTransfer provider"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateFleetCluster(spec FleetClusterSpec) []error {
	var errs []error
	if strings.TrimSpace(spec.ClusterID) == "" {
		errs = append(errs, ValidationError{"spec.clusterId", "must not be empty"})
	}
	if strings.TrimSpace(spec.TrustDomain) == "" {
		errs = append(errs, ValidationError{"spec.trustDomain", "must not be empty"})
	}
	if !validInfrastructureProvider(spec.InfrastructureProvider) {
		errs = append(errs, ValidationError{"spec.infrastructureProvider", "must be ModelPlane or DirectAgent"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

func ValidateFleetIntent(spec FleetIntentSpec, now time.Time) []error {
	var errs []error
	if strings.TrimSpace(spec.ActionClass) == "" {
		errs = append(errs, ValidationError{"spec.actionClass", "must not be empty"})
	}
	if strings.TrimSpace(spec.TargetRef.Name) == "" {
		errs = append(errs, ValidationError{"spec.targetRef.name", "must not be empty"})
	}
	if !sha256Pattern.MatchString(spec.DecisionPackageRef.SHA256) {
		errs = append(errs, ValidationError{"spec.decisionPackageRef.sha256", "must be a 64-character SHA-256 digest"})
	}
	if !spec.ExpiresAt.After(now) {
		errs = append(errs, ValidationError{"spec.expiresAt", "must be in the future"})
	}
	if strings.TrimSpace(spec.CorrelationID) == "" {
		errs = append(errs, ValidationError{"spec.correlationId", "must not be empty"})
	}
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		errs = append(errs, ValidationError{"spec.idempotencyKey", "must not be empty"})
	}
	if len(spec.Parameters) > MaxIntentParameterProperties {
		errs = append(errs, ValidationError{"spec.parameters", fmt.Sprintf("must contain no more than %d properties", MaxIntentParameterProperties)})
	}
	for name, value := range spec.Parameters {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, ValidationError{"spec.parameters", "property names must not be empty"})
		}
		if !json.Valid(value) {
			errs = append(errs, ValidationError{fmt.Sprintf("spec.parameters[%q]", name), "must contain valid JSON"})
		}
	}
	if raw, err := json.Marshal(spec.Parameters); err != nil {
		errs = append(errs, ValidationError{"spec.parameters", "must be JSON serializable"})
	} else if len(raw) > MaxIntentParametersBytes {
		errs = append(errs, ValidationError{"spec.parameters", fmt.Sprintf("serialized value must not exceed %d bytes", MaxIntentParametersBytes)})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	return errs
}

// ValidateFleetOperation validates a newly received operation. Planning and
// authorization are deliberately absent at creation and become required only
// at their corresponding lifecycle gates.
func ValidateFleetOperation(spec FleetOperationSpec, now time.Time) []error {
	return ValidateFleetOperationForPhase(spec, OperationReceived, now)
}

// ValidateFleetOperationForPhase enforces invariants at the transition where
// they first become meaningful. Authorization expiry is checked at the
// AUTHORIZED and ACTUATING gates, not against completed historical records.
func ValidateFleetOperationForPhase(spec FleetOperationSpec, phase OperationPhase, now time.Time) []error {
	var errs []error
	if !validOperationPhase(phase) {
		errs = append(errs, ValidationError{"status.phase", "is not a recognized operation phase"})
	}
	if strings.TrimSpace(spec.IntentRef.Name) == "" {
		errs = append(errs, ValidationError{"spec.intentRef.name", "must not be empty"})
	}
	if spec.PlanDigest != nil && !sha256Pattern.MatchString(*spec.PlanDigest) {
		errs = append(errs, ValidationError{"spec.planDigest", "must be a 64-character SHA-256 digest"})
	}
	if phaseRequiresPlan(phase) && spec.PlanDigest == nil {
		errs = append(errs, ValidationError{"spec.planDigest", "is required at PLANNED and later execution phases"})
	}
	if strings.TrimSpace(spec.ActionClass) == "" || strings.TrimSpace(spec.TargetRef.Name) == "" {
		errs = append(errs, ValidationError{"spec", "actionClass and targetRef.name must not be empty"})
	}
	if spec.Provider != nil && (strings.TrimSpace(spec.Provider.Type) == "" || strings.TrimSpace(spec.Provider.Name) == "") {
		errs = append(errs, ValidationError{"spec.provider", "type and name must not be empty"})
	}
	if phaseRequiresPlan(phase) && spec.Provider == nil {
		errs = append(errs, ValidationError{"spec.provider", "is required at PLANNED and later execution phases"})
	}
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		errs = append(errs, ValidationError{"spec.idempotencyKey", "must not be empty"})
	}
	errs = append(errs, ValidateAuthorizationReference(spec.AuthorizationRef)...)
	if spec.AuthorizationRef != nil {
		ref := spec.AuthorizationRef
		if ref.ActionClass != spec.ActionClass {
			errs = append(errs, ValidationError{"spec.authorizationRef.actionClass", "must match spec.actionClass"})
		}
		if spec.PlanDigest != nil && ref.SpecDigest != *spec.PlanDigest {
			errs = append(errs, ValidationError{"spec.authorizationRef.specDigest", "must match spec.planDigest"})
		}
		if ref.IdempotencyKey != spec.IdempotencyKey {
			errs = append(errs, ValidationError{"spec.authorizationRef.idempotencyKey", "must match spec.idempotencyKey"})
		}
	}
	if phaseRequiresAuthorization(phase) && spec.AuthorizationRef == nil {
		errs = append(errs, ValidationError{"spec.authorizationRef", "is required at AUTHORIZED and later execution phases"})
	}
	if phaseRequiresUnexpiredAuthorization(phase) && spec.AuthorizationRef != nil && !spec.AuthorizationRef.ExpiresAt.After(now) {
		errs = append(errs, ValidationError{"spec.authorizationRef.expiresAt", "must be in the future"})
	}
	return errs
}

// ValidateFleetOperationResourceForPhase adds object-identity binding to the
// spec-only lifecycle checks. Kubernetes assigns metadata.uid after creation,
// so the binding is enforced whenever that stable identity is available.
func ValidateFleetOperationResourceForPhase(operation FleetOperation, phase OperationPhase, now time.Time) []error {
	errs := ValidateFleetOperationForPhase(operation.Spec, phase, now)
	if operation.Spec.AuthorizationRef == nil || strings.TrimSpace(operation.Metadata.UID) == "" {
		return errs
	}
	if operation.Spec.AuthorizationRef.ObjectUID != operation.Metadata.UID {
		errs = append(errs, ValidationError{"spec.authorizationRef.objectUid", "must match metadata.uid"})
	}
	return errs
}

func phaseRequiresPlan(phase OperationPhase) bool {
	switch phase {
	case OperationPlanned, OperationGovernancePending, OperationAuthorized, OperationActuating,
		OperationObserving, OperationVerified, OperationEvidencePending, OperationSucceeded,
		OperationRollingBack, OperationRolledBack, OperationQuarantined:
		return true
	default:
		return false
	}
}

func phaseRequiresAuthorization(phase OperationPhase) bool {
	switch phase {
	case OperationAuthorized, OperationActuating, OperationObserving, OperationVerified,
		OperationEvidencePending, OperationSucceeded, OperationRollingBack, OperationRolledBack:
		return true
	default:
		return false
	}
}

func phaseRequiresUnexpiredAuthorization(phase OperationPhase) bool {
	return phase == OperationAuthorized || phase == OperationActuating
}

func ValidateAuthorizationReference(ref *AuthorizationReference) []error {
	if ref == nil {
		return nil
	}
	var errs []error
	required := []struct{ field, value string }{
		{"grantId", ref.GrantID}, {"subject", ref.Subject}, {"actionClass", ref.ActionClass},
		{"objectUid", ref.ObjectUID}, {"specDigest", ref.SpecDigest},
		{"audience", ref.Audience}, {"idempotencyKey", ref.IdempotencyKey},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			errs = append(errs, ValidationError{"authorizationRef." + item.field, "must not be empty"})
		}
	}
	if ref.SpecDigest != "" && !sha256Pattern.MatchString(ref.SpecDigest) {
		errs = append(errs, ValidationError{"authorizationRef.specDigest", "must be a 64-character SHA-256 digest"})
	}
	if ref.Audience != "" && ref.Audience != FleetAuthorizationAudience {
		errs = append(errs, ValidationError{"authorizationRef.audience", "must be fleet-llm-d"})
	}
	if ref.ExpiresAt.IsZero() {
		errs = append(errs, ValidationError{"authorizationRef.expiresAt", "must be set"})
	}
	if ref.BreakGlass && strings.TrimSpace(ref.IncidentRef) == "" {
		errs = append(errs, ValidationError{"authorizationRef.incidentRef", "is required for break-glass grants"})
	}
	return errs
}

func ValidateTransitions(transitions []OperationTransition) []error {
	var errs []error
	var previous int64
	for i, transition := range transitions {
		if i == 0 && transition.Sequence < 1 {
			errs = append(errs, ValidationError{fmt.Sprintf("status.transitions[%d].sequence", i), "must start at 1 or greater"})
		}
		if i > 0 && transition.Sequence != previous+1 {
			errs = append(errs, ValidationError{fmt.Sprintf("status.transitions[%d].sequence", i), "must be contiguous and strictly increasing"})
		}
		if !validOperationPhase(transition.Phase) {
			errs = append(errs, ValidationError{fmt.Sprintf("status.transitions[%d].phase", i), "is not a recognized operation phase"})
		}
		previous = transition.Sequence
	}
	return errs
}

func validInfrastructureProvider(provider InfrastructureProvider) bool {
	return provider == InfrastructureProviderModelPlane || provider == InfrastructureProviderDirectAgent
}
func validOperationPhase(phase OperationPhase) bool {
	switch phase {
	case OperationReceived, OperationAccepted, OperationPlanned, OperationGovernancePending, OperationAuthorized, OperationActuating, OperationObserving, OperationVerified, OperationEvidencePending, OperationSucceeded, OperationRejected, OperationDeferred, OperationExpired, OperationFailed, OperationRollingBack, OperationRolledBack, OperationQuarantined:
		return true
	default:
		return false
	}
}
