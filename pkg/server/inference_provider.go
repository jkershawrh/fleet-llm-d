package server

import (
	"fmt"
	"strings"
)

// InferenceProviderName identifies the internal data plane used after fleet
// policy has resolved a request's exact physical model.
type InferenceProviderName string

const (
	InferenceProviderPraxis InferenceProviderName = "praxis"
	InferenceProviderLLMD   InferenceProviderName = "llm-d-router"
)

// ParseInferenceProviderName validates the gateway data-plane selection.
func ParseInferenceProviderName(value string) (InferenceProviderName, error) {
	name := InferenceProviderName(strings.TrimSpace(value))
	if name == "" {
		return InferenceProviderPraxis, nil
	}
	switch name {
	case InferenceProviderPraxis, InferenceProviderLLMD:
		return name, nil
	default:
		return "", fmt.Errorf("unsupported inference provider %q (want praxis or llm-d-router)", value)
	}
}

type inferenceTarget struct {
	BaseURL  string
	APIToken string
	Provider InferenceProviderName
}

func (fc *FleetController) inferenceTarget(physicalModel string) (inferenceTarget, error) {
	provider := fc.InferenceProviderName
	if provider == "" {
		provider = InferenceProviderPraxis
	}
	target := inferenceTarget{Provider: provider}
	switch provider {
	case InferenceProviderPraxis:
		target.BaseURL = fc.PraxisURL
		target.APIToken = fc.PraxisToken
	case InferenceProviderLLMD:
		target.APIToken = fc.LLMDToken
		if physicalModel == fc.cpuPhysicalModel() {
			target.BaseURL = fc.LLMDCPUURL
		} else {
			target.BaseURL = fc.LLMDGPUURL
		}
	default:
		return inferenceTarget{}, fmt.Errorf("unsupported inference provider %q", provider)
	}
	target.BaseURL = strings.TrimRight(strings.TrimSpace(target.BaseURL), "/")
	if target.BaseURL == "" {
		return inferenceTarget{}, fmt.Errorf("%s endpoint is not configured for model %q", provider, physicalModel)
	}
	return target, nil
}
