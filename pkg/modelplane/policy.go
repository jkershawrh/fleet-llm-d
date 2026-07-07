package modelplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PolicyInjector patches ModelPlane resources with fleet policy constraints.
type PolicyInjector struct {
	apiServer string
	token     string
	http      *http.Client
}

// NewPolicyInjector creates a new PolicyInjector.
func NewPolicyInjector(apiServer, token string) *PolicyInjector {
	return &PolicyInjector{
		apiServer: apiServer,
		token:     token,
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
	}
}

// ApplyPlacementAnnotations patches a ModelDeployment with fleet placement constraints.
func (p *PolicyInjector) ApplyPlacementAnnotations(ctx context.Context, deploymentName, namespace string, constraints map[string]string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": constraints,
		},
	}
	return p.patchResource(ctx, fmt.Sprintf(
		"/apis/modelplane.ai/v1alpha1/namespaces/%s/modeldeployments/%s",
		namespace, deploymentName), patch)
}

// SetReplicaCount patches ModelDeployment.spec.replicas.
func (p *PolicyInjector) SetReplicaCount(ctx context.Context, deploymentName, namespace string, replicas int) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}
	return p.patchResource(ctx, fmt.Sprintf(
		"/apis/modelplane.ai/v1alpha1/namespaces/%s/modeldeployments/%s",
		namespace, deploymentName), patch)
}

// SetServiceWeights patches ModelService endpoint weights for canary routing.
func (p *PolicyInjector) SetServiceWeights(ctx context.Context, serviceName, namespace string, weights map[string]int) error {
	endpoints := make([]WeightedEndpoint, 0, len(weights))
	for name, weight := range weights {
		endpoints = append(endpoints, WeightedEndpoint{
			EndpointName: name,
			Weight:       weight,
		})
	}
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"endpoints": endpoints,
		},
	}
	return p.patchResource(ctx, fmt.Sprintf(
		"/apis/modelplane.ai/v1alpha1/namespaces/%s/modelservices/%s",
		namespace, serviceName), patch)
}

// patchResource sends a JSON merge patch to the ModelPlane API.
func (p *PolicyInjector) patchResource(ctx context.Context, path string, patch interface{}) error {
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshalling patch: %w", err)
	}

	url := p.apiServer + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("executing patch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("patch failed with status %d: %s", resp.StatusCode, snippet)
	}

	return nil
}
