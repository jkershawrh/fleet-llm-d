package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// KubernetesConfigMapPublisher atomically replaces a ConfigMap's data map.
// Kubernetes projected volumes expose each update through an atomic symlink
// swap, which the Router's watched file discovery plugin supports.
type KubernetesConfigMapPublisher struct {
	apiURL    string
	namespace string
	name      string
	token     string
	client    *http.Client
}

func NewKubernetesConfigMapPublisher(apiURL, namespace, name, token string, client *http.Client) (*KubernetesConfigMapPublisher, error) {
	if _, err := url.ParseRequestURI(apiURL); err != nil || !strings.HasPrefix(apiURL, "https://") && !strings.HasPrefix(apiURL, "http://") {
		return nil, fmt.Errorf("invalid Kubernetes API URL")
	}
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("ConfigMap namespace and name are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &KubernetesConfigMapPublisher{apiURL: strings.TrimRight(apiURL, "/"), namespace: namespace, name: name, token: token, client: client}, nil
}

func (p *KubernetesConfigMapPublisher) Publish(ctx context.Context, files map[string][]byte) error {
	data := make(map[string]string, len(files))
	for name, contents := range files {
		data[name] = string(contents)
	}
	body, err := json.Marshal(map[string]interface{}{"data": data})
	if err != nil {
		return fmt.Errorf("marshal Router ConfigMap patch: %w", err)
	}
	endpoint := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps/%s", p.apiURL, url.PathEscape(p.namespace), url.PathEscape(p.name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("publish Router ConfigMap: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("publish Router ConfigMap: Kubernetes API returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return nil
}
