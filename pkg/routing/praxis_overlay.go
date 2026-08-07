package routing

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

// PraxisClusterEndpoint maps a fleet cluster to a Praxis backend endpoint.
type PraxisClusterEndpoint struct {
	ClusterID string
	Endpoint  string // host:port
	TLS       bool
}

// PraxisOverlay generates a Praxis AI gateway config from fleet placement
// and routing decisions. The output is a YAML string suitable for writing
// to the praxis-ai-config ConfigMap.
type PraxisOverlay struct {
	ListenAddress string
	Endpoints     map[string]PraxisClusterEndpoint // clusterID → endpoint
}

// NewPraxisOverlay creates an overlay renderer with the given cluster endpoints.
func NewPraxisOverlay(endpoints []PraxisClusterEndpoint) *PraxisOverlay {
	m := make(map[string]PraxisClusterEndpoint, len(endpoints))
	for _, ep := range endpoints {
		m[ep.ClusterID] = ep
	}
	return &PraxisOverlay{
		ListenAddress: "0.0.0.0:8080",
		Endpoints:     m,
	}
}

// PoolPlacement represents a model placed on specific clusters.
type PoolPlacement struct {
	ModelName string
	ModelAliases []string
	Clusters  []string // cluster IDs where the model is placed
}

// RenderConfig generates the full Praxis AI config YAML from the current
// set of pool placements. Each model gets a routing rule per alias and
// a load_balancer cluster per placed cluster.
func (o *PraxisOverlay) RenderConfig(placements []PoolPlacement) (string, error) {
	routes := make([]map[string]interface{}, 0)
	clusters := make([]map[string]interface{}, 0)
	clusterSeen := make(map[string]bool)

	for _, p := range placements {
		clusterName := sanitizeClusterName(p.ModelName)

		endpoints := make([]string, 0)
		for _, cid := range p.Clusters {
			ep, ok := o.Endpoints[cid]
			if !ok {
				continue
			}
			endpoints = append(endpoints, ep.Endpoint)
		}
		if len(endpoints) == 0 {
			continue
		}

		allNames := append([]string{p.ModelName}, p.ModelAliases...)
		for _, name := range allNames {
			route := map[string]interface{}{
				"path_prefix": "/",
				"headers":     map[string]string{"x-ai-model": name},
				"cluster":     clusterName,
			}
			routes = append(routes, route)
		}

		if !clusterSeen[clusterName] {
			cluster := map[string]interface{}{
				"name":      clusterName,
				"endpoints": endpoints,
			}
			clusters = append(clusters, cluster)
			clusterSeen[clusterName] = true
		}
	}

	if len(routes) > 0 && len(clusters) > 0 {
		defaultRoute := map[string]interface{}{
			"path_prefix": "/",
			"cluster":     sanitizeClusterName(placements[0].ModelName),
		}
		routes = append(routes, defaultRoute)
	}

	config := map[string]interface{}{
		"listeners": []map[string]interface{}{
			{
				"name":          "fleet-inference-gateway",
				"address":       o.ListenAddress,
				"filter_chains": []string{"inference-pipeline"},
			},
		},
		"filter_chains": []map[string]interface{}{
			{
				"name": "inference-pipeline",
				"filters": []map[string]interface{}{
					{"filter": "model_to_header", "header": "X-AI-Model"},
					{"filter": "router", "routes": routes},
					{"filter": "token_count", "provider": "openai"},
					{"filter": "token_usage_headers"},
					{"filter": "access_log"},
					{"filter": "load_balancer", "clusters": clusters},
				},
			},
		},
	}

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal praxis config: %w", err)
	}
	return string(out), nil
}

// RenderFromRouteDecisions generates a Praxis config from a list of
// fleet routing evaluator decisions paired with cluster health data.
// This is the integration point between the fleet routing policy
// evaluator and the Praxis config overlay.
func (o *PraxisOverlay) RenderFromRouteDecisions(
	decisions []policy.RouteDecision,
	health []policy.ClusterHealth,
) (string, error) {
	placements := make([]PoolPlacement, 0)
	seen := make(map[string]*PoolPlacement)

	for _, d := range decisions {
		key := d.Reason // model name is stored in Reason by convention
		if key == "" {
			continue
		}
		if p, ok := seen[key]; ok {
			p.Clusters = append(p.Clusters, d.TargetCluster)
		} else {
			p := &PoolPlacement{
				ModelName: key,
				Clusters:  []string{d.TargetCluster},
			}
			seen[key] = p
			placements = append(placements, *p)
		}
	}

	return o.RenderConfig(placements)
}

func sanitizeClusterName(name string) string {
	r := strings.NewReplacer("/", "-", ".", "-", " ", "-")
	return r.Replace(strings.ToLower(name))
}

// PlacementsFromReconciler converts the reconciler's pool state into
// PoolPlacement structs for overlay rendering.
func PlacementsFromReconciler(pools map[string]struct {
	Model           string
	DesiredClusters []string
}) []PoolPlacement {
	placements := make([]PoolPlacement, 0, len(pools))
	for _, p := range pools {
		if len(p.DesiredClusters) == 0 {
			continue
		}
		placements = append(placements, PoolPlacement{
			ModelName: p.Model,
			Clusters:  p.DesiredClusters,
		})
	}
	return placements
}
