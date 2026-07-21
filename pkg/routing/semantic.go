package routing

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// SemanticClassification holds the result from either the vLLM Semantic Router
// or the GCL prompt classifier fallback.
type SemanticClassification struct {
	Tier       string  `json:"tier"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// SemanticRouter classifies prompts and maps tiers to model names.
type SemanticRouter struct {
	classifierURL string
	tierModels    map[string]string // tier -> model name
	enabled       bool
}

// NewSemanticRouter creates a semantic router. classifierURL is the URL of the
// GCL classify-prompt endpoint (e.g. http://gcl-app.governed-cognitive-loop.svc:8000).
// tierModels maps tier names to registered model names.
func NewSemanticRouter(classifierURL string, tierModels map[string]string) *SemanticRouter {
	enabled := classifierURL != "" && len(tierModels) > 0
	if enabled {
		slog.Info("semantic routing enabled: classifier=%s tiers=%v", classifierURL, tierModels)
	}
	return &SemanticRouter{
		classifierURL: classifierURL,
		tierModels:    tierModels,
		enabled:       enabled,
	}
}

// Classify sends a prompt to the classifier and returns the tier and mapped model.
// Returns empty strings if classification fails or is not configured.
func (sr *SemanticRouter) Classify(prompt string) (model string, tier string, confidence float64, err error) {
	if !sr.enabled {
		return "", "", 0, nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	body := fmt.Sprintf(`{"prompt":%q}`, prompt)
	resp, err := client.Post(
		sr.classifierURL+"/api/v1/classify-prompt",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		return "", "", 0, fmt.Errorf("semantic classifier call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", 0, fmt.Errorf("semantic classifier returned %d", resp.StatusCode)
	}

	var classification SemanticClassification
	if err := json.NewDecoder(resp.Body).Decode(&classification); err != nil {
		return "", "", 0, fmt.Errorf("failed to decode classification: %w", err)
	}

	mappedModel, ok := sr.tierModels[classification.Tier]
	if !ok {
		// Unknown tier, fall through to default
		return "", classification.Tier, classification.Confidence, nil
	}

	return mappedModel, classification.Tier, classification.Confidence, nil
}
