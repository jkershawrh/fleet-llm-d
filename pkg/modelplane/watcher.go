package modelplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// ModelPlaneWatcher polls the ModelPlane API for CRD changes and invokes
// registered callbacks when resources are created, modified, or deleted.
type ModelPlaneWatcher struct {
	apiServer string
	namespace string
	token     string
	interval  time.Duration
	http      *http.Client

	mu                 sync.Mutex
	onClusterChange    func([]InferenceCluster)
	onDeploymentChange func([]ModelDeployment)
	onEndpointChange   func([]ModelEndpoint)

	lastClusters    []InferenceCluster
	lastDeployments []ModelDeployment
	lastEndpoints   []ModelEndpoint
}

// NewModelPlaneWatcher creates a new watcher that polls the ModelPlane API.
func NewModelPlaneWatcher(apiServer, namespace, token string) *ModelPlaneWatcher {
	return &ModelPlaneWatcher{
		apiServer: apiServer,
		namespace: namespace,
		token:     token,
		interval:  30 * time.Second,
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

// SetInterval sets the polling interval.
func (w *ModelPlaneWatcher) SetInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// OnClusterChange registers a callback for InferenceCluster changes.
func (w *ModelPlaneWatcher) OnClusterChange(fn func([]InferenceCluster)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onClusterChange = fn
}

// OnDeploymentChange registers a callback for ModelDeployment changes.
func (w *ModelPlaneWatcher) OnDeploymentChange(fn func([]ModelDeployment)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onDeploymentChange = fn
}

// OnEndpointChange registers a callback for ModelEndpoint changes.
func (w *ModelPlaneWatcher) OnEndpointChange(fn func([]ModelEndpoint)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onEndpointChange = fn
}

// Start begins polling the ModelPlane API. It performs an initial poll then
// starts a background goroutine that polls on each tick of the interval.
func (w *ModelPlaneWatcher) Start(ctx context.Context) error {
	if err := w.pollOnce(ctx); err != nil {
		log.Printf("WARNING: initial ModelPlane poll failed: %v (will retry on next tick)", err)
	}

	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		log.Println("ModelPlane watcher started")
		defer log.Println("ModelPlane watcher stopped")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.pollOnce(ctx); err != nil {
					log.Printf("ModelPlane poll error: %v", err)
				}
			}
		}
	}()

	return nil
}

// pollOnce fetches all three resource types from the ModelPlane API and
// invokes callbacks when changes are detected.
func (w *ModelPlaneWatcher) pollOnce(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Poll clusters
	clusters, err := fetchClusters(ctx, w.http, w.apiServer, w.token,
		"/apis/modelplane.ai/v1alpha1/inferenceclusters")
	if err != nil {
		return fmt.Errorf("polling clusters: %w", err)
	}
	if w.onClusterChange != nil && !clustersEqual(w.lastClusters, clusters) {
		w.onClusterChange(clusters)
	}
	w.lastClusters = clusters

	// Poll deployments
	deployments, err := fetchDeployments(ctx, w.http, w.apiServer, w.token,
		fmt.Sprintf("/apis/modelplane.ai/v1alpha1/namespaces/%s/modeldeployments", w.namespace))
	if err != nil {
		return fmt.Errorf("polling deployments: %w", err)
	}
	if w.onDeploymentChange != nil && !deploymentsEqual(w.lastDeployments, deployments) {
		w.onDeploymentChange(deployments)
	}
	w.lastDeployments = deployments

	// Poll endpoints
	endpoints, err := fetchEndpoints(ctx, w.http, w.apiServer, w.token,
		fmt.Sprintf("/apis/modelplane.ai/v1alpha1/namespaces/%s/modelendpoints", w.namespace))
	if err != nil {
		return fmt.Errorf("polling endpoints: %w", err)
	}
	if w.onEndpointChange != nil && !endpointsEqual(w.lastEndpoints, endpoints) {
		w.onEndpointChange(endpoints)
	}
	w.lastEndpoints = endpoints

	return nil
}

// clusterList is the API response wrapper for InferenceCluster resources.
type clusterList struct {
	Items []InferenceCluster `json:"items"`
}

// deploymentList is the API response wrapper for ModelDeployment resources.
type deploymentList struct {
	Items []ModelDeployment `json:"items"`
}

// endpointList is the API response wrapper for ModelEndpoint resources.
type endpointList struct {
	Items []ModelEndpoint `json:"items"`
}

// fetchClusters fetches a list of InferenceCluster resources from the ModelPlane API.
func fetchClusters(ctx context.Context, client *http.Client, apiServer, token, path string) ([]InferenceCluster, error) {
	body, err := doFetch(ctx, client, apiServer, token, path)
	if err != nil {
		return nil, err
	}
	var list clusterList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}
	return list.Items, nil
}

// fetchDeployments fetches a list of ModelDeployment resources from the ModelPlane API.
func fetchDeployments(ctx context.Context, client *http.Client, apiServer, token, path string) ([]ModelDeployment, error) {
	body, err := doFetch(ctx, client, apiServer, token, path)
	if err != nil {
		return nil, err
	}
	var list deploymentList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}
	return list.Items, nil
}

// fetchEndpoints fetches a list of ModelEndpoint resources from the ModelPlane API.
func fetchEndpoints(ctx context.Context, client *http.Client, apiServer, token, path string) ([]ModelEndpoint, error) {
	body, err := doFetch(ctx, client, apiServer, token, path)
	if err != nil {
		return nil, err
	}
	var list endpointList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}
	return list.Items, nil
}

// doFetch performs the HTTP GET request and returns the response body bytes.
func doFetch(ctx context.Context, client *http.Client, apiServer, token, path string) ([]byte, error) {
	url := apiServer + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, snippet)
	}

	return body, nil
}

// Change detection helpers using JSON comparison.
func clustersEqual(a, b []InferenceCluster) bool {
	return jsonEqual(a, b)
}

func deploymentsEqual(a, b []ModelDeployment) bool {
	return jsonEqual(a, b)
}

func endpointsEqual(a, b []ModelEndpoint) bool {
	return jsonEqual(a, b)
}

func jsonEqual(a, b interface{}) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aj) == string(bj)
}

// LastClusters returns the most recently polled clusters (for API handlers).
func (w *ModelPlaneWatcher) LastClusters() []InferenceCluster {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]InferenceCluster, len(w.lastClusters))
	copy(cp, w.lastClusters)
	return cp
}

// LastDeployments returns the most recently polled deployments (for API handlers).
func (w *ModelPlaneWatcher) LastDeployments() []ModelDeployment {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]ModelDeployment, len(w.lastDeployments))
	copy(cp, w.lastDeployments)
	return cp
}
