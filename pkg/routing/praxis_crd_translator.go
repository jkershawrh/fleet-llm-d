package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
)

// GridCRDTranslator watches fleet-llm-d CRDs and renders corresponding
// Praxis Grid CRDs. The translation is unidirectional: fleet → Grid.
// Grid CRDs are applied via the K8s API using server-side apply.
type GridCRDTranslator struct {
	apiServer   string
	namespace   string
	token       string
	httpClient  *http.Client
	gridNetwork string
}

// Name identifies this translator as the Praxis routing-provider adapter.
func (t *GridCRDTranslator) Name() ProviderName { return ProviderPraxis }

// Sync implements RoutingProvider without changing the existing Praxis CRDs.
func (t *GridCRDTranslator) Sync(ctx context.Context, clusters []FleetClusterInfo, pools []FleetPoolInfo) error {
	return t.SyncFromFleetState(ctx, clusters, pools)
}

// NewGridCRDTranslator creates a translator that writes Grid CRDs to the
// given K8s API server. The gridNetwork parameter names the GridNetwork
// resource that owns the translated GridSite and InferenceProvider CRDs.
func NewGridCRDTranslator(apiServer, namespace, token, gridNetwork string) *GridCRDTranslator {
	tlsConfig, err := tlsutil.NewTLSConfig(tlsutil.KubernetesTLSOptions())
	if err != nil {
		slog.Warn("grid translator: failed to load Kubernetes CA, falling back to system CA", "error", err)
		var fallbackErr error
		tlsConfig, fallbackErr = tlsutil.NewTLSConfig(tlsutil.TLSOptions{})
		if fallbackErr != nil {
			slog.Error("grid translator: system CA fallback also failed", "error", fallbackErr)
		}
	}
	return &GridCRDTranslator{
		apiServer:   strings.TrimRight(apiServer, "/"),
		namespace:   namespace,
		token:       token,
		gridNetwork: gridNetwork,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}
}

// GridSiteSpec is the spec for a grid.praxis-proxy.io/v1alpha1 GridSite.
type GridSiteSpec struct {
	GridNetworkRef  string      `json:"gridNetworkRef"`
	Region          string      `json:"region,omitempty"`
	Zone            string      `json:"zone,omitempty"`
	SovereigntyZone string      `json:"sovereigntyZone,omitempty"`
	Egress          *GridEgress `json:"egress,omitempty"`
}

type GridEgress struct {
	Address string         `json:"address"`
	TLS     *GridEgressTLS `json:"tls,omitempty"`
}

type GridEgressTLS struct {
	Mode       string `json:"mode,omitempty"`
	ServerName string `json:"serverName,omitempty"`
}

// InferenceProviderSpec is the spec for a grid.praxis-proxy.io/v1alpha1 InferenceProvider.
type InferenceProviderSpec struct {
	GridNetworkRef string                `json:"gridNetworkRef"`
	ProviderKind   string                `json:"providerKind"`
	BackendKind    string                `json:"backendKind"`
	Endpoint       string                `json:"endpoint"`
	Models         []InferenceModelEntry `json:"models,omitempty"`
	MetricsConfig  *MetricsConfig        `json:"metricsConfig,omitempty"`
	SiteSelector   *LabelSelector        `json:"siteSelector,omitempty"`
}

type InferenceModelEntry struct {
	Name          string   `json:"name"`
	Capabilities  []string `json:"capabilities,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
}

type MetricsConfig struct {
	MetricsEndpoint     string            `json:"metricsEndpoint,omitempty"`
	Path                string            `json:"path,omitempty"`
	Timeout             string            `json:"timeout,omitempty"`
	PoolName            string            `json:"poolName,omitempty"`
	QueueCapacity       int               `json:"queueCapacity,omitempty"`
	StaleMetricsSeconds int               `json:"staleMetricsSeconds,omitempty"`
	SignalNames         map[string]string `json:"signalNames,omitempty"`
}

type LabelSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

type LabelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// FleetClusterInfo holds the fleet-llm-d cluster state needed for translation.
type FleetClusterInfo struct {
	ID              string
	Name            string
	Region          string
	Labels          map[string]string
	EgressAddress   string
	MetricsEndpoint string
	Transport       EndpointTransport
	Status          string
	UpdatedAt       time.Time
	Draining        bool
	Authorized      bool
	GPUTypes        []string
	GPUAvailable    int
	GPUTotal        int
}

func (c FleetClusterInfo) routingEndpoint() string {
	if value := strings.TrimSpace(c.Transport.RoutingURL); value != "" {
		return value
	}
	return strings.TrimSpace(c.EgressAddress)
}

func (c FleetClusterInfo) metricsEndpoint() string {
	if value := strings.TrimSpace(c.Transport.MetricsURL); value != "" {
		return value
	}
	return strings.TrimSpace(c.MetricsEndpoint)
}

func (c FleetClusterInfo) tlsServerName() string {
	if value := strings.TrimSpace(c.Transport.TLSServerName); value != "" {
		return value
	}
	return strings.TrimSpace(c.Labels["fleet.llm-d.ai/tls-server-name"])
}

// FleetPoolInfo holds the fleet-llm-d pool state needed for translation.
type FleetPoolInfo struct {
	Name            string
	ModelName       string
	PhysicalModel   string
	ModelSource     string
	Clusters        []string
	TargetPorts     []int
	MetricsEndpoint string
}

// TranslateClusterToGridSite converts a FleetCluster into a GridSite spec.
func (t *GridCRDTranslator) TranslateClusterToGridSite(cluster FleetClusterInfo) GridSiteSpec {
	spec := GridSiteSpec{
		GridNetworkRef: t.gridNetwork,
		Region:         cluster.Region,
	}
	if zone, ok := cluster.Labels["topology.kubernetes.io/zone"]; ok {
		spec.Zone = zone
	}
	if sz, ok := cluster.Labels["fleet.llm-d.ai/sovereignty-zone"]; ok {
		spec.SovereigntyZone = sz
	}
	if endpoint := cluster.routingEndpoint(); endpoint != "" {
		spec.Egress = &GridEgress{
			Address: endpoint,
			TLS:     &GridEgressTLS{Mode: "Mutual", ServerName: cluster.tlsServerName()},
		}
	}
	return spec
}

// TranslatePoolToInferenceProvider converts a FleetInferencePool into an InferenceProvider spec.
func (t *GridCRDTranslator) TranslatePoolToInferenceProvider(pool FleetPoolInfo) InferenceProviderSpec {
	spec := InferenceProviderSpec{
		GridNetworkRef: t.gridNetwork,
		ProviderKind:   "InCluster",
		BackendKind:    "local",
		Models: []InferenceModelEntry{
			{Name: pool.ModelName, Capabilities: []string{"chat", "completions"}},
		},
	}

	if len(pool.TargetPorts) > 0 {
		spec.Endpoint = fmt.Sprintf("http://%s.%s.svc:%d",
			pool.Name, t.namespace, pool.TargetPorts[0])
		if pool.MetricsEndpoint == "" {
			pool.MetricsEndpoint = spec.Endpoint
		}
	}

	if pool.MetricsEndpoint != "" {
		spec.MetricsConfig = &MetricsConfig{
			MetricsEndpoint:     pool.MetricsEndpoint,
			Path:                "/metrics",
			Timeout:             "2s",
			PoolName:            pool.ModelName,
			QueueCapacity:       64,
			StaleMetricsSeconds: 30,
			SignalNames: map[string]string{
				"queueDepth":         "llm_d_router_epp_average_queue_size",
				"kvCacheUtilization": "llm_d_router_epp_average_kv_cache_utilization",
				"healthy":            "llm_d_router_epp_ready_endpoints",
			},
		}
	}

	if len(pool.Clusters) > 0 {
		if len(pool.Clusters) == 1 {
			spec.SiteSelector = &LabelSelector{MatchLabels: map[string]string{
				"fleet.llm-d.ai/cluster-id": pool.Clusters[0],
			}}
		} else {
			spec.SiteSelector = &LabelSelector{MatchExpressions: []LabelSelectorRequirement{{
				Key: "fleet.llm-d.ai/cluster-id", Operator: "In", Values: append([]string(nil), pool.Clusters...),
			}}}
		}
	}

	return spec
}

// ApplyGridSite creates or updates a GridSite CRD via the K8s API.
func (t *GridCRDTranslator) ApplyGridSite(ctx context.Context, name string, spec GridSiteSpec) error {
	resource := map[string]interface{}{
		"apiVersion": "grid.praxis-proxy.io/v1alpha1",
		"kind":       "GridSite",
		"metadata": map[string]interface{}{
			"name": name,
			"labels": map[string]string{
				"fleet.llm-d.ai/managed-by": "fleet-controller",
				"fleet.llm-d.ai/cluster-id": name,
			},
		},
		"spec": spec,
	}
	return t.applyResource(ctx, "gridsites", name, resource)
}

// ApplyInferenceProvider creates or updates an InferenceProvider CRD via the K8s API.
func (t *GridCRDTranslator) ApplyInferenceProvider(ctx context.Context, name string, spec InferenceProviderSpec) error {
	resource := map[string]interface{}{
		"apiVersion": "grid.praxis-proxy.io/v1alpha1",
		"kind":       "InferenceProvider",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": t.namespace,
			"labels": map[string]string{
				"fleet.llm-d.ai/managed-by": "fleet-controller",
			},
		},
		"spec": spec,
	}
	return t.applyNamespacedResource(ctx, "inferenceproviders", name, resource)
}

// SyncFromFleetState translates all fleet clusters and pools to Grid CRDs.
func (t *GridCRDTranslator) SyncFromFleetState(ctx context.Context, clusters []FleetClusterInfo, pools []FleetPoolInfo) error {
	var errs []string

	for _, cluster := range clusters {
		spec := t.TranslateClusterToGridSite(cluster)
		if err := t.ApplyGridSite(ctx, cluster.ID, spec); err != nil {
			errs = append(errs, fmt.Sprintf("GridSite %s: %v", cluster.ID, err))
		}
	}

	for _, pool := range pools {
		spec := t.TranslatePoolToInferenceProvider(pool)
		if err := t.ApplyInferenceProvider(ctx, pool.Name, spec); err != nil {
			errs = append(errs, fmt.Sprintf("InferenceProvider %s: %v", pool.Name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("grid CRD sync errors: %s", strings.Join(errs, "; "))
	}

	slog.Info("Grid CRD sync complete", "sites", len(clusters), "providers", len(pools))
	return nil
}

func (t *GridCRDTranslator) applyResource(ctx context.Context, resource, name string, body interface{}) error {
	url := fmt.Sprintf("%s/apis/grid.praxis-proxy.io/v1alpha1/%s/%s",
		t.apiServer, resource, name)
	return t.patchResource(ctx, url, body)
}

func (t *GridCRDTranslator) applyNamespacedResource(ctx context.Context, resource, name string, body interface{}) error {
	url := fmt.Sprintf("%s/apis/grid.praxis-proxy.io/v1alpha1/namespaces/%s/%s/%s",
		t.apiServer, t.namespace, resource, name)
	return t.patchResource(ctx, url, body)
}

func (t *GridCRDTranslator) patchResource(ctx context.Context, url string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url+"?fieldManager=fleet-controller&force=true", strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/apply-patch+yaml")
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("apply: %d", resp.StatusCode)
	}
	return nil
}
