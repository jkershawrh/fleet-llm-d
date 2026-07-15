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

	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
)

// ModelPlaneWatcher polls the ModelPlane API for CRD changes and invokes
// registered callbacks when resources are created, modified, or deleted.
type ModelPlaneWatcher struct {
	apiServer string
	namespace string
	token     string
	interval  time.Duration
	http      *http.Client

	mu                 sync.RWMutex
	onClusterChange    func([]InferenceCluster)
	onDeploymentChange func([]ModelDeployment)
	onEndpointChange   func([]ModelEndpoint)

	lastClusters    []InferenceCluster
	lastDeployments []ModelDeployment
	lastEndpoints   []ModelEndpoint
}

// NewModelPlaneWatcher creates a new watcher that polls the ModelPlane API.
// An optional tlsutil.TLSOptions can be passed to configure TLS behavior.
// Verification is enabled by default; insecure mode requires explicit opt-in.
func NewModelPlaneWatcher(apiServer, namespace, token string, tlsOpts ...tlsutil.TLSOptions) *ModelPlaneWatcher {
	opts := tlsutil.TLSOptions{}
	if len(tlsOpts) > 0 {
		opts = tlsOpts[0]
	}

	tlsCfg, err := tlsutil.NewTLSConfig(opts)
	if err != nil {
		log.Printf("failed to build configured ModelPlane TLS trust: %v", err)
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	tlsCfg.MinVersion = tls.VersionTLS13

	return &ModelPlaneWatcher{
		apiServer: apiServer,
		namespace: namespace,
		token:     token,
		interval:  30 * time.Second,
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
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
	go w.Run(ctx)
	return nil
}

// Run polls ModelPlane until ctx is cancelled. It blocks so a leadership
// coordinator can wait until the watcher has fully stopped before restarting.
func (w *ModelPlaneWatcher) Run(ctx context.Context) {
	if err := w.PollOnce(ctx); err != nil {
		log.Printf("WARNING: initial ModelPlane poll failed: %v (will retry on next tick)", err)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	log.Println("ModelPlane watcher started")
	defer log.Println("ModelPlane watcher stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.PollOnce(ctx); err != nil {
				log.Printf("ModelPlane poll error: %v", err)
			}
		}
	}
}

// PollOnce fetches all three resource types from the ModelPlane API and
// invokes callbacks when changes are detected. HTTP fetches happen outside
// the lock so that LastClusters/LastDeployments/LastEndpoints are never
// blocked during network I/O.
func (w *ModelPlaneWatcher) PollOnce(ctx context.Context) error {
	// Fetch all three resources without holding the lock.
	clusters, err := fetchClusters(ctx, w.http, w.apiServer, w.token,
		"/apis/modelplane.ai/v1alpha1/inferenceclusters")
	if err != nil {
		return fmt.Errorf("polling clusters: %w", err)
	}

	deployments, err := fetchDeployments(ctx, w.http, w.apiServer, w.token,
		fmt.Sprintf("/apis/modelplane.ai/v1alpha1/namespaces/%s/modeldeployments", w.namespace))
	if err != nil {
		return fmt.Errorf("polling deployments: %w", err)
	}

	endpoints, err := fetchEndpoints(ctx, w.http, w.apiServer, w.token,
		fmt.Sprintf("/apis/modelplane.ai/v1alpha1/namespaces/%s/modelendpoints", w.namespace))
	if err != nil {
		return fmt.Errorf("polling endpoints: %w", err)
	}

	// Briefly write-lock to swap cached data and detect changes.
	w.mu.Lock()
	var clusterCB func([]InferenceCluster)
	var deployCB func([]ModelDeployment)
	var endpointCB func([]ModelEndpoint)

	if w.onClusterChange != nil && !clustersEqual(w.lastClusters, clusters) {
		clusterCB = w.onClusterChange
	}
	w.lastClusters = clusters

	if w.onDeploymentChange != nil && !deploymentsEqual(w.lastDeployments, deployments) {
		deployCB = w.onDeploymentChange
	}
	w.lastDeployments = deployments

	if w.onEndpointChange != nil && !endpointsEqual(w.lastEndpoints, endpoints) {
		endpointCB = w.onEndpointChange
	}
	w.lastEndpoints = endpoints
	w.mu.Unlock()

	// Invoke callbacks outside the lock to avoid holding it during
	// potentially expensive handler logic.
	if clusterCB != nil {
		clusterCB(clusters)
	}
	if deployCB != nil {
		deployCB(deployments)
	}
	if endpointCB != nil {
		endpointCB(endpoints)
	}

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
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
	w.mu.RLock()
	defer w.mu.RUnlock()
	cp := make([]InferenceCluster, len(w.lastClusters))
	copy(cp, w.lastClusters)
	return cp
}

// LastDeployments returns the most recently polled deployments (for API handlers).
func (w *ModelPlaneWatcher) LastDeployments() []ModelDeployment {
	w.mu.RLock()
	defer w.mu.RUnlock()
	cp := make([]ModelDeployment, len(w.lastDeployments))
	copy(cp, w.lastDeployments)
	return cp
}

// LastEndpoints returns the most recently polled endpoints (for API handlers).
func (w *ModelPlaneWatcher) LastEndpoints() []ModelEndpoint {
	w.mu.RLock()
	defer w.mu.RUnlock()
	cp := make([]ModelEndpoint, len(w.lastEndpoints))
	copy(cp, w.lastEndpoints)
	return cp
}
