package actuator

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

// ModelPlaneActuator scales ModelDeployments via the ModelPlane API.
type ModelPlaneActuator struct {
	apiServer string
	token     string
	http      *http.Client
}

// NewModelPlaneActuator creates a new actuator that patches ModelDeployment
// replica counts through the ModelPlane API server.
func NewModelPlaneActuator(apiServer, token string) *ModelPlaneActuator {
	return &ModelPlaneActuator{
		apiServer: apiServer,
		token:     token,
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // #nosec G402 -- in-cluster self-signed certs
				},
			},
		},
	}
}

// ScaleDeployment patches the replica count of a ModelDeployment.
func (a *ModelPlaneActuator) ScaleDeployment(ctx context.Context, name, namespace string, replicas int) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}

	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshalling scale patch: %w", err)
	}

	url := fmt.Sprintf("%s/apis/modelplane.ai/v1alpha1/namespaces/%s/modeldeployments/%s",
		a.apiServer, namespace, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating scale request: %w", err)
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("executing scale request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("scale failed with status %d: %s", resp.StatusCode, snippet)
	}

	return nil
}
