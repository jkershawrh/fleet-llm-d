package routing

import (
	"context"
	"fmt"
	"strings"
)

// EndpointTransport is the portable connection contract between fleet policy
// and a routing adapter. Platform-specific reconcilers (for example an
// OpenShift Route or a Gateway API implementation) publish these values; the
// routing provider consumes them without needing to know which platform
// produced the endpoint.
type EndpointTransport struct {
	RoutingURL    string
	MetricsURL    string
	TLSServerName string
	TrustRef      string
	AuthRef       string
}

// ProviderName identifies the single routing data-plane adapter that receives
// fleet-qualified provider state from the controller.
type ProviderName string

const (
	ProviderPraxis ProviderName = "praxis"
	ProviderLLMD   ProviderName = "llm-d-router"
	ProviderNone   ProviderName = "disabled"
)

// ParseProviderName validates a routing-provider configuration value.
func ParseProviderName(value string) (ProviderName, error) {
	name := ProviderName(strings.TrimSpace(value))
	if name == "" {
		return ProviderPraxis, nil
	}
	switch name {
	case ProviderPraxis, ProviderLLMD, ProviderNone:
		return name, nil
	default:
		return "", fmt.Errorf("unsupported routing provider %q (want praxis, llm-d-router, or disabled)", value)
	}
}

// RoutingProvider translates an already-qualified fleet provider set into a
// product-specific routing representation. Implementations must not add
// clusters or change model eligibility.
type RoutingProvider interface {
	Name() ProviderName
	Sync(ctx context.Context, clusters []FleetClusterInfo, pools []FleetPoolInfo) error
}
