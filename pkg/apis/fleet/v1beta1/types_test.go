package v1beta1

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

func TestProviderDefaults(t *testing.T) {
	pool := FleetInferencePoolSpec{}
	DefaultFleetInferencePool(&pool)
	if pool.InfrastructureProvider != InfrastructureProviderModelPlane {
		t.Fatalf("pool provider = %q, want %q", pool.InfrastructureProvider, InfrastructureProviderModelPlane)
	}

	routing := FleetRoutingPolicySpec{}
	DefaultFleetRoutingPolicy(&routing)
	if routing.Provider != RoutingProviderModelPlaneGateway {
		t.Fatalf("routing provider = %q, want %q", routing.Provider, RoutingProviderModelPlaneGateway)
	}

	kv := KVCacheTransferPolicySpec{}
	DefaultKVCacheTransferPolicy(&kv)
	if kv.Provider != KVCacheProviderLlmDNative || kv.Transport.Protocol != TransferProtocolGRPC || kv.Transport.FallbackPolicy != TransferFallbackDeny {
		t.Fatalf("unexpected KV defaults: %#v", kv)
	}
}

func TestAlphaConversionPreservesLegacyProviderOwnership(t *testing.T) {
	alphaPool := v1alpha1.FleetInferencePoolSpec{
		Model:     v1alpha1.ModelSpec{Name: "model-a"},
		Placement: v1alpha1.PlacementRef{PolicyRef: "placement-a"},
	}
	betaPool, err := ConvertFleetInferencePoolFromV1Alpha1(alphaPool, "fleet-a")
	if err != nil {
		t.Fatal(err)
	}
	if betaPool.InfrastructureProvider != InfrastructureProviderDirectAgent || betaPool.HomeFleet != "fleet-a" {
		t.Fatalf("legacy pool ownership changed during conversion: %#v", betaPool)
	}

	betaRouting, err := ConvertFleetRoutingPolicyFromV1Alpha1(v1alpha1.FleetRoutingPolicySpec{Strategy: "Weighted"})
	if err != nil {
		t.Fatal(err)
	}
	if betaRouting.Provider != RoutingProviderFleetGateway {
		t.Fatalf("legacy routing provider = %q", betaRouting.Provider)
	}

	betaKV, err := ConvertKVCacheTransferPolicyFromV1Alpha1(v1alpha1.KVCacheTransferPolicySpec{
		Transport: v1alpha1.TransportSpec{Protocol: "NIXL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if betaKV.Provider != KVCacheProviderFleetTransfer || betaKV.Transport.Protocol != TransferProtocolNIXL {
		t.Fatalf("legacy KV ownership changed during conversion: %#v", betaKV)
	}
}

func TestDestructiveAlphaDownConversionIsRejected(t *testing.T) {
	_, err := ConvertFleetInferencePoolToV1Alpha1(FleetInferencePoolSpec{
		InfrastructureProvider: InfrastructureProviderModelPlane,
	})
	if err == nil {
		t.Fatal("expected ModelPlane down-conversion to be rejected")
	}
}

func TestValidateProviderCompatibility(t *testing.T) {
	kv := KVCacheTransferPolicySpec{
		Provider: KVCacheProviderLlmDNative,
		Transport: TransportSpec{
			Protocol:       TransferProtocolNIXL,
			FallbackPolicy: TransferFallbackDeny,
		},
	}
	errs := ValidateKVCacheTransferPolicy(kv)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "NIXL requires") {
		t.Fatalf("expected NIXL/provider compatibility error, got %v", errs)
	}

	kv.Provider = KVCacheProviderFleetTransfer
	if errs := ValidateKVCacheTransferPolicy(kv); len(errs) != 0 {
		t.Fatalf("valid FleetTransfer/NIXL policy rejected: %v", errs)
	}
}

func TestValidateAuthorizationReference(t *testing.T) {
	now := time.Now().UTC()
	ref := &AuthorizationReference{
		GrantID:        "grant-1",
		ActionClass:    "fleet.deploy",
		SpecDigest:     strings.Repeat("a", 64),
		Audience:       "fleet-controller",
		ExpiresAt:      now.Add(time.Hour),
		IdempotencyKey: "intent-1",
		BreakGlass:     true,
	}
	errs := ValidateAuthorizationReference(ref)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "incidentRef") {
		t.Fatalf("expected break-glass incident error, got %v", errs)
	}

	ref.IncidentRef = "incident-42"
	if errs := ValidateAuthorizationReference(ref); len(errs) != 0 {
		t.Fatalf("valid authorization rejected: %v", errs)
	}
}

func TestValidateTransitionJournal(t *testing.T) {
	now := time.Now().UTC()
	valid := []OperationTransition{
		{Sequence: 1, Phase: OperationReceived, At: now, Reason: "IntentReceived"},
		{Sequence: 2, Phase: OperationAccepted, At: now, Reason: "IntentAccepted"},
	}
	if errs := ValidateTransitions(valid); len(errs) != 0 {
		t.Fatalf("valid transition journal rejected: %v", errs)
	}

	invalid := append([]OperationTransition(nil), valid...)
	invalid[1].Sequence = 3
	if errs := ValidateTransitions(invalid); len(errs) != 1 {
		t.Fatalf("expected one sequence error, got %v", errs)
	}
}

func TestFleetOperationValidationIsPhaseAware(t *testing.T) {
	now := time.Now().UTC()
	spec := FleetOperationSpec{
		IntentRef:      LocalObjectReference{Name: "intent-1"},
		ActionClass:    "fleet.scale",
		TargetRef:      NamespacedObjectReference{Name: "pool-1"},
		IdempotencyKey: "operation-1",
	}
	if errs := ValidateFleetOperation(spec, now); len(errs) != 0 {
		t.Fatalf("new operation must not require plan or authorization: %v", errs)
	}
	if errs := ValidateFleetOperationForPhase(spec, OperationPlanned, now); len(errs) != 2 {
		t.Fatalf("PLANNED must require plan digest and provider, got %v", errs)
	}

	digest := strings.Repeat("c", 64)
	provider := &ProviderReference{Type: "ModelPlane", Name: "modelplane-primary"}
	spec.PlanDigest = &digest
	spec.Provider = provider
	if errs := ValidateFleetOperationForPhase(spec, OperationPlanned, now); len(errs) != 0 {
		t.Fatalf("planned operation rejected: %v", errs)
	}
	if errs := ValidateFleetOperationForPhase(spec, OperationAuthorized, now); len(errs) != 1 || !strings.Contains(errs[0].Error(), "authorizationRef") {
		t.Fatalf("AUTHORIZED must require authorization, got %v", errs)
	}

	spec.AuthorizationRef = &AuthorizationReference{
		GrantID:        "grant-1",
		ActionClass:    "fleet.scale",
		SpecDigest:     digest,
		Audience:       "fleet-controller",
		ExpiresAt:      now.Add(-time.Minute),
		IdempotencyKey: "operation-1",
	}
	if errs := ValidateFleetOperationForPhase(spec, OperationAuthorized, now); len(errs) != 1 || !strings.Contains(errs[0].Error(), "must be in the future") {
		t.Fatalf("AUTHORIZED must reject expired authorization, got %v", errs)
	}
	spec.AuthorizationRef.ExpiresAt = now.Add(time.Minute)
	if errs := ValidateFleetOperationForPhase(spec, OperationActuating, now); len(errs) != 0 {
		t.Fatalf("ACTUATING operation with a live grant rejected: %v", errs)
	}

	// Historical success remains valid after the once-live grant expires.
	spec.AuthorizationRef.ExpiresAt = now.Add(-time.Minute)
	if errs := ValidateFleetOperationForPhase(spec, OperationSucceeded, now); len(errs) != 0 {
		t.Fatalf("completed operation must not be invalidated by later grant expiry: %v", errs)
	}
}

func TestFleetOperationCreationOmitsDeferredFields(t *testing.T) {
	spec := FleetOperationSpec{
		IntentRef:      LocalObjectReference{Name: "intent-1"},
		ActionClass:    "fleet.scale",
		TargetRef:      NamespacedObjectReference{Name: "pool-1"},
		IdempotencyKey: "operation-1",
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"planDigest", "provider", "authorizationRef"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Errorf("new operation unexpectedly serialized deferred field %s: %s", field, raw)
		}
	}
}

func TestIntentParametersAreOpaqueAndBounded(t *testing.T) {
	now := time.Now().UTC()
	spec := FleetIntentSpec{
		ActionClass: "fleet.scale",
		TargetRef:   NamespacedObjectReference{Name: "pool-1"},
		DecisionPackageRef: DecisionPackageReference{
			URI:    "are://decision-package/1",
			SHA256: strings.Repeat("d", 64),
		},
		Proposer:       ProposerAuthority{Subject: "spiffe://test/proposer", AttestationRef: "att-1", Ceiling: "fleet.scale"},
		ExpiresAt:      now.Add(time.Hour),
		CorrelationID:  "correlation-1",
		IdempotencyKey: "intent-1",
		Parameters: IntentParameters{
			"desired_replicas": json.RawMessage(`4`),
			"routing":          json.RawMessage(`{"prefer_local":true}`),
		},
	}
	if errs := ValidateFleetIntent(spec, now); len(errs) != 0 {
		t.Fatalf("valid opaque intent parameters rejected: %v", errs)
	}

	spec.Parameters["invalid"] = json.RawMessage(`{`)
	if errs := ValidateFleetIntent(spec, now); len(errs) == 0 {
		t.Fatal("invalid JSON parameter must be rejected")
	}
	delete(spec.Parameters, "invalid")
	for i := 0; i <= MaxIntentParameterProperties; i++ {
		spec.Parameters[fmt.Sprintf("parameter-%d", i)] = json.RawMessage(`true`)
	}
	if errs := ValidateFleetIntent(spec, now); len(errs) == 0 {
		t.Fatal("parameter property limit must be enforced")
	}

	spec.Parameters = IntentParameters{
		"payload": json.RawMessage(`"` + strings.Repeat("x", MaxIntentParametersBytes) + `"`),
	}
	if errs := ValidateFleetIntent(spec, now); len(errs) == 0 {
		t.Fatal("serialized parameter byte limit must be enforced")
	}
}

func TestCommonStatusFieldsMarshalAtStatusRoot(t *testing.T) {
	when := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	status := FleetOperationStatus{
		CommonStatus: CommonStatus{
			ObservedGeneration:           7,
			SpecDigest:                   strings.Repeat("b", 64),
			CorrelationID:                "correlation-1",
			LastSuccessfulReconciliation: &when,
		},
		Phase: OperationVerified,
	}

	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"observedGeneration", "specDigest", "correlationId", "lastSuccessfulReconciliation", "phase"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("marshaled status missing %q: %s", field, raw)
		}
	}
}
