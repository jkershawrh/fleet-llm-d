package actuator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
)

// ModelPlaneActuator scales ModelDeployments via the ModelPlane API.
type ModelPlaneActuator struct {
	apiServer string
	token     string
	http      *http.Client
}

// NewModelPlaneActuator creates a new actuator that patches ModelDeployment
// replica counts through the ModelPlane API server.
// An optional tlsutil.TLSOptions can be passed to configure TLS behavior;
// when omitted, InsecureSkipVerify is used for backward compatibility.
func NewModelPlaneActuator(apiServer, token string, tlsOpts ...tlsutil.TLSOptions) *ModelPlaneActuator {
	opts := tlsutil.TLSOptions{InsecureSkipVerify: true}
	if len(tlsOpts) > 0 {
		opts = tlsOpts[0]
	}

	tlsCfg, err := tlsutil.NewTLSConfig(opts)
	if err != nil {
		log.Printf("WARNING: failed to build TLS config: %v, falling back to InsecureSkipVerify", err)
		tlsCfg = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // fallback
	}

	return &ModelPlaneActuator{
		apiServer: apiServer,
		token:     token,
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
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

	reqURL := fmt.Sprintf("%s/apis/modelplane.ai/v1alpha1/namespaces/%s/modeldeployments/%s",
		a.apiServer, url.PathEscape(namespace), url.PathEscape(name))

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, reqURL, bytes.NewReader(body))
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
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("scale failed with status %d: %s", resp.StatusCode, snippet)
	}

	return nil
}
