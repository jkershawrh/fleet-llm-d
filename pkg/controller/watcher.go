package controller

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
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
	ready    atomic.Bool

	routingMu       sync.RWMutex
	routingPolicies map[string]v1alpha1.FleetRoutingPolicySpec
}

// k8sPoolList represents the Kubernetes API list response for FleetInferencePool CRDs.
type k8sPoolList struct {
	Items []k8sPoolItem `json:"items"`
}

type k8sPoolItem struct {
	Metadata k8sMetadata                     `json:"metadata"`
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
// An optional tlsutil.TLSOptions can be passed to configure a custom CA.
// Certificate verification is always enabled.
func NewCRDWatcher(apiServer, namespace, token string, reconciler *Reconciler, tlsOpts ...tlsutil.TLSOptions) *CRDWatcher {
	opts := tlsutil.KubernetesTLSOptions()
	if len(tlsOpts) > 0 {
		opts = tlsOpts[0]
	}

	tlsCfg, err := tlsutil.NewTLSConfig(opts)
	if err != nil {
		slog.Warn("failed to build configured TLS trust", "error", err)
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	tlsCfg.MinVersion = tls.VersionTLS13

	w := &CRDWatcher{
		apiServer:    apiServer,
		namespace:    namespace,
		token:        token,
		reconciler:   reconciler,
		pollInterval: 30 * time.Second,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
		lastSeen: make(map[string]v1alpha1.FleetInferencePoolSpec),
	}
	reconciler.SetPlacementPolicyResolver(w.getPlacementPolicy)
	return w
}

type k8sPlacementPolicy struct {
	Spec v1alpha1.PlacementPolicySpec `json:"spec"`
}

func (w *CRDWatcher) getPlacementPolicy(ctx context.Context, ref string) (v1alpha1.PlacementPolicySpec, error) {
	if ref == "" {
		return v1alpha1.PlacementPolicySpec{}, fmt.Errorf("placement policy reference is required")
	}
	url := fmt.Sprintf("%s/apis/fleet.llm-d.ai/v1alpha1/namespaces/%s/placementpolicies/%s",
		w.apiServer, w.namespace, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return v1alpha1.PlacementPolicySpec{}, fmt.Errorf("creating placement policy request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return v1alpha1.PlacementPolicySpec{}, fmt.Errorf("fetching placement policy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return v1alpha1.PlacementPolicySpec{}, fmt.Errorf("placement policy %q returned %d: %s", ref, resp.StatusCode, string(body))
	}
	var resource k8sPlacementPolicy
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&resource); err != nil {
		return v1alpha1.PlacementPolicySpec{}, fmt.Errorf("decoding placement policy %q: %w", ref, err)
	}
	return resource.Spec, nil
}

// GetScalingPolicy fetches a FleetScalingPolicy CRD by name from the K8s API.
func (w *CRDWatcher) GetScalingPolicy(ref string) (*v1alpha1.FleetScalingPolicySpec, error) {
	if ref == "" {
		return nil, fmt.Errorf("scaling policy reference is required")
	}
	url := fmt.Sprintf("%s/apis/fleet.llm-d.ai/v1alpha1/namespaces/%s/fleetscalingpolicies/%s",
		w.apiServer, w.namespace, ref)
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating scaling policy request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching scaling policy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("scaling policy %q returned %d: %s", ref, resp.StatusCode, string(body))
	}
	var resource struct {
		Spec v1alpha1.FleetScalingPolicySpec `json:"spec"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&resource); err != nil {
		return nil, fmt.Errorf("decoding scaling policy %q: %w", ref, err)
	}
	return &resource.Spec, nil
}

// GetRoutingPolicy returns a cached FleetRoutingPolicy by name. If name is
// empty, returns the first policy found (for backward compatibility).
func (w *CRDWatcher) GetRoutingPolicy(name ...string) *v1alpha1.FleetRoutingPolicySpec {
	w.routingMu.RLock()
	defer w.routingMu.RUnlock()
	if len(name) > 0 && name[0] != "" {
		if p, ok := w.routingPolicies[name[0]]; ok {
			return &p
		}
		return nil
	}
	for _, p := range w.routingPolicies {
		spec := p
		return &spec
	}
	return nil
}

func (w *CRDWatcher) pollRoutingPolicy(ctx context.Context) {
	url := fmt.Sprintf("%s/apis/fleet.llm-d.ai/v1alpha1/namespaces/%s/fleetroutingpolicies",
		w.apiServer, w.namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var list struct {
		Items []struct {
			Metadata k8sMetadata                       `json:"metadata"`
			Spec     v1alpha1.FleetRoutingPolicySpec `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err != nil {
		return
	}
	w.routingMu.Lock()
	defer w.routingMu.Unlock()
	w.routingPolicies = make(map[string]v1alpha1.FleetRoutingPolicySpec, len(list.Items))
	for _, item := range list.Items {
		w.routingPolicies[item.Metadata.Name] = item.Spec
	}
	if len(list.Items) > 0 {
		slog.Info("routing policies loaded", "count", len(list.Items))
	}
}

// Start begins polling the Kubernetes API for CRD changes in the background.
// The goroutine exits when ctx is cancelled. Start returns nil immediately.
func (w *CRDWatcher) Start(ctx context.Context) error {
	go w.Run(ctx)
	return nil
}

// Run polls the Kubernetes API until ctx is cancelled. Unlike Start, Run
// blocks so callers coordinating leadership can wait for complete shutdown.
func (w *CRDWatcher) Run(ctx context.Context) {
	if err := w.pollOnce(ctx); err != nil {
		slog.Warn("initial CRD poll failed, will retry on next tick", "error", err)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	slog.Info("CRD watcher started")
	defer slog.Info("CRD watcher stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.pollOnce(ctx); err != nil {
				slog.Warn("CRD poll error", "error", err)
			}
		}
	}
}

// pollOnce fetches the current list of FleetInferencePool CRDs from the
// Kubernetes API, compares them with the last-seen state, and calls the
// reconciler for any additions, modifications, or deletions.
func (w *CRDWatcher) pollOnce(ctx context.Context) (err error) {
	defer func() { w.ready.Store(err == nil) }()
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
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
	nextSeen := make(map[string]v1alpha1.FleetInferencePoolSpec, len(w.lastSeen)+len(current))
	for name, spec := range w.lastSeen {
		nextSeen[name] = spec
	}

	// Detect additions and modifications.
	for name, spec := range current {
		prev, exists := w.lastSeen[name]
		if !exists {
			added++
			if err := w.reconciler.ReconcilePool(ctx, spec); err != nil {
				slog.Info("reconcile (add) %q failed: %v", name, err)
				continue
			}
			nextSeen[name] = spec
			continue
		}

		changed, err := specsChanged(prev, spec)
		if err != nil {
			slog.Info("spec comparison for %q failed: %v", name, err)
			continue
		}
		if changed {
			modified++
			if err := w.reconciler.ReconcilePool(ctx, spec); err != nil {
				slog.Info("reconcile (modify) %q failed: %v", name, err)
				continue
			}
			nextSeen[name] = spec
		}
	}

	// Detect deletions.
	for name := range w.lastSeen {
		if _, exists := current[name]; !exists {
			deleted++
			if err := w.reconciler.DeletePool(ctx, name); err != nil {
				slog.Info("reconcile (delete) %q failed: %v", name, err)
				continue
			}
			delete(nextSeen, name)
		}
	}

	w.lastSeen = nextSeen

	slog.Info("polled pools", "total", len(current), "added", added, "modified", modified, "deleted", deleted)

	w.pollRoutingPolicy(ctx)

	return nil
}

// Ready reports whether the most recent Kubernetes API poll completed. It is
// used by the controller readiness probe so a missing CRD/API dependency cannot
// be hidden behind a live HTTP process.
func (w *CRDWatcher) Ready() bool {
	return w.ready.Load()
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
