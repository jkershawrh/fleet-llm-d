package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LLMDClusterEndpoint matches llm-d Router's multicluster-file-discovery
// endpoint schema. Product-specific metadata stays in labels so the upstream
// reader can ignore fields it does not yet consume.
type LLMDClusterEndpoint struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Address   string            `json:"address"`
	Port      string            `json:"port"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type llmdEndpointsFile struct {
	Endpoints []LLMDClusterEndpoint `json:"endpoints"`
}

type llmdModelIndex struct {
	Version string            `json:"version"`
	Models  map[string]string `json:"models"`
}

type LLMDProviderOptions struct {
	Directory    string
	Namespace    string
	MaxStaleness time.Duration
	RequireTLS   bool
	Now          func() time.Time
}

// LLMDProvider renders one watched endpoint file per exact physical model.
// Keeping model candidate sets separate preserves the Router's current
// single-model EPP assumption and prevents cross-model scoring.
type LLMDProvider struct {
	opts LLMDProviderOptions
}

func NewLLMDProvider(opts LLMDProviderOptions) (*LLMDProvider, error) {
	if !filepath.IsAbs(opts.Directory) {
		return nil, fmt.Errorf("llm-d Router endpoint directory must be absolute")
	}
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.MaxStaleness <= 0 {
		opts.MaxStaleness = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &LLMDProvider{opts: opts}, nil
}

func (p *LLMDProvider) Name() ProviderName { return ProviderLLMD }

func (p *LLMDProvider) Sync(_ context.Context, clusters []FleetClusterInfo, pools []FleetPoolInfo) error {
	byID := make(map[string]FleetClusterInfo, len(clusters))
	for _, cluster := range clusters {
		byID[cluster.ID] = cluster
	}

	byModel := make(map[string][]LLMDClusterEndpoint)
	for _, pool := range pools {
		model := strings.TrimSpace(pool.PhysicalModel)
		if model == "" {
			model = strings.TrimSpace(pool.ModelName)
		}
		if model == "" {
			continue
		}
		for _, clusterID := range pool.Clusters {
			cluster, ok := byID[clusterID]
			if !ok || !p.eligible(cluster) {
				continue
			}
			endpoint, err := p.endpoint(cluster, pool, model)
			if err != nil {
				return fmt.Errorf("render model %q cluster %q: %w", model, clusterID, err)
			}
			byModel[model] = append(byModel[model], endpoint)
		}
	}

	if err := os.MkdirAll(p.opts.Directory, 0o750); err != nil {
		return fmt.Errorf("create llm-d Router endpoint directory: %w", err)
	}
	index := llmdModelIndex{Version: "v1", Models: make(map[string]string, len(byModel))}
	models := make([]string, 0, len(byModel))
	for model := range byModel {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		endpoints := byModel[model]
		sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Name < endpoints[j].Name })
		filename := modelFilename(model)
		if err := writeJSONAtomic(filepath.Join(p.opts.Directory, filename), llmdEndpointsFile{Endpoints: endpoints}); err != nil {
			return err
		}
		index.Models[model] = filename
	}
	// Publish the index last so readers never observe a model file before its
	// complete contents have been atomically installed.
	return writeJSONAtomic(filepath.Join(p.opts.Directory, "index.json"), index)
}

func (p *LLMDProvider) eligible(cluster FleetClusterInfo) bool {
	if !cluster.Authorized && cluster.Labels != nil && cluster.Labels["fleet.llm-d.ai/authorized"] == "false" {
		return false
	}
	if cluster.Draining || strings.EqualFold(cluster.Status, "draining") || cluster.Labels["fleet.llm-d.ai/draining"] == "true" {
		return false
	}
	switch strings.ToLower(cluster.Status) {
	case "unavailable", "unreachable", "failed", "left", "offline":
		return false
	}
	if !cluster.UpdatedAt.IsZero() && p.opts.Now().Sub(cluster.UpdatedAt) > p.opts.MaxStaleness {
		return false
	}
	return strings.TrimSpace(cluster.EgressAddress) != ""
}

func (p *LLMDProvider) endpoint(cluster FleetClusterInfo, pool FleetPoolInfo, model string) (LLMDClusterEndpoint, error) {
	routingURL, err := parseEndpoint(cluster.EgressAddress, pool.TargetPorts)
	if err != nil {
		return LLMDClusterEndpoint{}, err
	}
	if p.opts.RequireTLS && routingURL.Scheme != "https" {
		return LLMDClusterEndpoint{}, fmt.Errorf("routing endpoint must use HTTPS")
	}
	metricsRaw := cluster.MetricsEndpoint
	if metricsRaw == "" && cluster.Labels != nil {
		metricsRaw = cluster.Labels["fleet.llm-d.ai/metrics-endpoint"]
	}
	if metricsRaw == "" {
		metricsRaw = cluster.EgressAddress
	}
	metricsURL, err := parseEndpoint(metricsRaw, pool.TargetPorts)
	if err != nil {
		return LLMDClusterEndpoint{}, fmt.Errorf("metrics endpoint: %w", err)
	}
	if p.opts.RequireTLS && metricsURL.Scheme != "https" {
		return LLMDClusterEndpoint{}, fmt.Errorf("metrics endpoint must use HTTPS")
	}

	labels := map[string]string{
		"model":                         model,
		"fleet.llm-d.ai/cluster-id":     cluster.ID,
		"fleet.llm-d.ai/physical-model": model,
		"fleet.llm-d.ai/health":         normalizedHealth(cluster),
		"fleet.llm-d.ai/failure-domain": failureDomain(cluster),
		"fleet.llm-d.ai/tls-verify":     strconv.FormatBool(routingURL.Scheme == "https"),
		"metricsAddress":                metricsURL.Hostname(),
		"metricsPort":                   endpointPort(metricsURL),
	}
	if serverName := cluster.Labels["fleet.llm-d.ai/tls-server-name"]; serverName != "" {
		labels["fleet.llm-d.ai/tls-server-name"] = serverName
	}
	return LLMDClusterEndpoint{
		Name:      cluster.ID + "--" + pool.Name,
		Namespace: p.opts.Namespace,
		Address:   routingURL.Hostname(),
		Port:      endpointPort(routingURL),
		Labels:    labels,
	}, nil
}

func parseEndpoint(raw string, targetPorts []int) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid endpoint %q", raw)
	}
	if u.Port() == "" && len(targetPorts) > 0 {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(targetPorts[0]))
	}
	return u, nil
}

func endpointPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}

func failureDomain(cluster FleetClusterInfo) string {
	if cluster.Labels != nil {
		if zone := cluster.Labels["topology.kubernetes.io/zone"]; zone != "" {
			return zone
		}
	}
	return cluster.Region
}

func normalizedHealth(cluster FleetClusterInfo) string {
	switch strings.ToLower(cluster.Status) {
	case "degraded", "degraded/non-ha":
		return "degraded/non-HA"
	default:
		return "healthy"
	}
}

func modelFilename(model string) string {
	sum := sha256.Sum256([]byte(model))
	return "endpoints-" + hex.EncodeToString(sum[:6]) + ".json"
}

func writeJSONAtomic(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fleet-router-*")
	if err != nil {
		return fmt.Errorf("create temporary endpoint file: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o640); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish endpoint file: %w", err)
	}
	ok = true
	return nil
}
