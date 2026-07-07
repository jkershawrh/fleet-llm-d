package controller

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

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// CRDWatcher polls the Kubernetes API for FleetInferencePool CRD changes
// and feeds them to the Reconciler for processing.
type CRDWatcher struct {
	apiServer    string
	namespace    string
	token        string
	reconciler   *Reconciler
	pollInterval time.Duration
	httpClient   *http.Client

	mu       sync.Mutex
	lastSeen map[string]v1alpha1.FleetInferencePoolSpec // keyed by metadata.name
}

// k8sPoolList represents the Kubernetes API list response for FleetInferencePool CRDs.
type k8sPoolList struct {
	Items []k8sPoolItem `json:"items"`
}

type k8sPoolItem struct {
	Metadata k8sMetadata                    `json:"metadata"`
	Spec     v1alpha1.FleetInferencePoolSpec `json:"spec"`
}

type k8sMetadata struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	ResourceVersion string `json:"resourceVersion"`
}

// CRDWatcherOption is a functional option for configuring a CRDWatcher.
type CRDWatcherOption func(*CRDWatcher)

// WithPollInterval sets the polling interval for the CRD watcher.
func WithPollInterval(d time.Duration) CRDWatcherOption {
	return func(w *CRDWatcher) { w.pollInterval = d }
}

// WithHTTPClient sets a custom HTTP client for the CRD watcher.
func WithHTTPClient(c *http.Client) CRDWatcherOption {
	return func(w *CRDWatcher) { w.httpClient = c }
}

// NewCRDWatcher creates a new CRDWatcher that polls the Kubernetes API for
// FleetInferencePool CRD changes and reconciles them via the given Reconciler.
func NewCRDWatcher(apiServer, namespace, token string, reconciler *Reconciler, opts ...CRDWatcherOption) *CRDWatcher {
	w := &CRDWatcher{
		apiServer:    apiServer,
		namespace:    namespace,
		token:        token,
		reconciler:   reconciler,
		pollInterval: 30 * time.Second,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // #nosec G402 -- in-cluster self-signed certs
				},
			},
		},
		lastSeen: make(map[string]v1alpha1.FleetInferencePoolSpec),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Start begins polling the Kubernetes API for CRD changes. It performs an
// initial poll, then starts a background goroutine that polls on each tick
// of the configured interval. The goroutine exits when ctx is cancelled.
// Start returns nil immediately.
func (w *CRDWatcher) Start(ctx context.Context) error {
	if err := w.pollOnce(ctx); err != nil {
		log.Printf("WARNING: initial CRD poll failed: %v (will retry on next tick)", err)
	}

	go func() {
		ticker := time.NewTicker(w.pollInterval)
		defer ticker.Stop()

		log.Println("CRD watcher started")
		defer log.Println("CRD watcher stopped")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.pollOnce(ctx); err != nil {
					log.Printf("CRD poll error: %v", err)
				}
			}
		}
	}()

	return nil
}

// pollOnce fetches the current list of FleetInferencePool CRDs from the
// Kubernetes API, compares them with the last-seen state, and calls the
// reconciler for any additions, modifications, or deletions.
func (w *CRDWatcher) pollOnce(ctx context.Context) error {
	url := fmt.Sprintf("%s/apis/fleet.llm-d.ai/v1alpha1/namespaces/%s/fleetinferencepools",
		w.apiServer, w.namespace)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.token)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, snippet)
	}

	var poolList k8sPoolList
	if err := json.Unmarshal(body, &poolList); err != nil {
		return fmt.Errorf("unmarshalling pool list: %w", err)
	}

	// Build current state map.
	current := make(map[string]v1alpha1.FleetInferencePoolSpec, len(poolList.Items))
	for _, item := range poolList.Items {
		current[item.Metadata.Name] = item.Spec
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var added, modified, deleted int

	// Detect additions and modifications.
	for name, spec := range current {
		prev, exists := w.lastSeen[name]
		if !exists {
			added++
			if err := w.reconciler.ReconcilePool(ctx, spec); err != nil {
				log.Printf("reconcile (add) %q failed: %v", name, err)
			}
			continue
		}

		changed, err := specsChanged(prev, spec)
		if err != nil {
			log.Printf("spec comparison for %q failed: %v", name, err)
			continue
		}
		if changed {
			modified++
			if err := w.reconciler.ReconcilePool(ctx, spec); err != nil {
				log.Printf("reconcile (modify) %q failed: %v", name, err)
			}
		}
	}

	// Detect deletions.
	for name := range w.lastSeen {
		if _, exists := current[name]; !exists {
			deleted++
			if err := w.reconciler.DeletePool(ctx, name); err != nil {
				log.Printf("reconcile (delete) %q failed: %v", name, err)
			}
		}
	}

	w.lastSeen = current

	log.Printf("polled %d pools, %d added, %d modified, %d deleted",
		len(current), added, modified, deleted)

	return nil
}

// specsChanged returns true if the two specs differ. Comparison is done by
// marshalling both to JSON and comparing the resulting byte slices.
func specsChanged(a, b v1alpha1.FleetInferencePoolSpec) (bool, error) {
	aj, err := json.Marshal(a)
	if err != nil {
		return false, fmt.Errorf("marshalling old spec: %w", err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false, fmt.Errorf("marshalling new spec: %w", err)
	}
	return string(aj) != string(bj), nil
}
