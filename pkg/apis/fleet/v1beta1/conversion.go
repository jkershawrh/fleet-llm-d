package v1beta1

import (
	"encoding/json"
	"fmt"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// Conversion helpers centralize the compatibility policy until a Kubernetes
// conversion webhook wires these contracts into admission. Alpha resources
// retain the direct/custom providers they historically represented.
func ConvertFleetInferencePoolFromV1Alpha1(in v1alpha1.FleetInferencePoolSpec, homeFleet string) (FleetInferencePoolSpec, error) {
	var out FleetInferencePoolSpec
	if strings.TrimSpace(homeFleet) == "" {
		return out, fmt.Errorf("homeFleet is required when converting a v1alpha1 FleetInferencePool")
	}
	if err := convertJSON(in, &out); err != nil {
		return out, err
	}
	out.InfrastructureProvider = InfrastructureProviderDirectAgent
	out.HomeFleet = homeFleet
	return out, nil
}

func ConvertFleetRoutingPolicyFromV1Alpha1(in v1alpha1.FleetRoutingPolicySpec) (FleetRoutingPolicySpec, error) {
	var out FleetRoutingPolicySpec
	if err := convertJSON(in, &out); err != nil {
		return out, err
	}
	out.Provider = RoutingProviderPraxisGateway
	return out, nil
}

func ConvertKVCacheTransferPolicyFromV1Alpha1(in v1alpha1.KVCacheTransferPolicySpec) (KVCacheTransferPolicySpec, error) {
	var out KVCacheTransferPolicySpec
	if err := convertJSON(in, &out); err != nil {
		return out, err
	}
	out.Provider = KVCacheProviderFleetTransfer
	switch {
	case strings.EqualFold(string(out.Transport.Protocol), "nixl"):
		out.Transport.Protocol = TransferProtocolNIXL
	case strings.EqualFold(string(out.Transport.Protocol), "tcp"):
		out.Transport.Protocol = TransferProtocolTCP
	default:
		out.Transport.Protocol = TransferProtocolGRPC
	}
	out.Transport.FallbackPolicy = TransferFallbackDeny
	return out, nil
}

// Safe down-conversion helpers reject beta-only ownership or governance fields
// instead of silently changing who actuates a resource.
func ConvertFleetInferencePoolToV1Alpha1(in FleetInferencePoolSpec) (v1alpha1.FleetInferencePoolSpec, error) {
	var out v1alpha1.FleetInferencePoolSpec
	if in.InfrastructureProvider != InfrastructureProviderDirectAgent {
		return out, fmt.Errorf("cannot down-convert infrastructureProvider %q to v1alpha1", in.InfrastructureProvider)
	}
	if err := rejectAuthorization(in.AuthorizationRef); err != nil {
		return out, err
	}
	return out, convertJSON(in, &out)
}

func rejectAuthorization(ref *AuthorizationReference) error {
	if ref != nil {
		return fmt.Errorf("cannot down-convert authorizationRef to v1alpha1 without losing governance state")
	}
	return nil
}

func convertJSON(in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal conversion source: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unmarshal conversion target: %w", err)
	}
	return nil
}
