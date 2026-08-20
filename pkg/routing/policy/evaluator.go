package policy

import (
	"context"
	"fmt"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// RoutingRequest captures the incoming request context used for routing decisions.
type RoutingRequest struct {
	Model        string
	ModelID      string
	TenantID     string
	Headers      map[string]string
	SourceRegion string
	Region       string
	TokenCount   int
	Priority     string
	SessionID    string

	// AllowedClusters restricts routing to only these clusters when non-empty.
	// Populated from TenantProfile.spec.clusters.allowed.
	AllowedClusters []string
	// DeniedClusters excludes these clusters from routing consideration.
	// Populated from TenantProfile.spec.clusters.denied.
	DeniedClusters []string

	// SemanticLabel is the top classification label from llm-d-sc (e.g. SIMPLE, REASONING).
	SemanticLabel string
	// SemanticScore is the cosine similarity score of the top label.
	SemanticScore float64
	// SemanticMargin is the score gap between the top two labels (confidence signal).
	SemanticMargin float64
}

// RouteDecision describes where to send a request and why.
type RouteDecision struct {
	TargetCluster   string
	Weight          float64
	HeadersToInject map[string]string
	Reason          string
}

// ClusterHealth represents the observed health and capacity of a single cluster.
type ClusterHealth struct {
	ClusterID         string
	Healthy           bool
	LatencyMs         float64
	CapacityRemaining float64
	KVCacheHitRate    float64
	AvailableSlots    int
	CurrentLoad       float64
	CostPerToken      float64
	Region            string
	PoolSaturation    float64 // EPP flow-control pool saturation (0-1)
}

// RoutingPolicyEvaluator evaluates a FleetRoutingPolicySpec against a request
// and a set of candidate clusters to produce a routing decision.
type RoutingPolicyEvaluator interface {
	Evaluate(ctx context.Context, request RoutingRequest, clusters []ClusterHealth, policy v1alpha1.FleetRoutingPolicySpec) (RouteDecision, error)
}

type defaultRoutingPolicyEvaluator struct{}

// NewRoutingPolicyEvaluator returns a default RoutingPolicyEvaluator.
func NewRoutingPolicyEvaluator() RoutingPolicyEvaluator {
	return &defaultRoutingPolicyEvaluator{}
}

// Evaluate matches the request against routing rules and applies the
// corresponding action to select a target cluster. When the request carries
// tenant cluster restrictions (AllowedClusters / DeniedClusters from the
// TenantProfile CRD), the candidate list is filtered before evaluation.
func (e *defaultRoutingPolicyEvaluator) Evaluate(ctx context.Context, request RoutingRequest, clusters []ClusterHealth, policy v1alpha1.FleetRoutingPolicySpec) (RouteDecision, error) {
	// Apply tenant cluster restrictions before any routing logic.
	clusters = filterClustersByTenant(clusters, request.AllowedClusters, request.DeniedClusters)

	for _, rule := range policy.Rules {
		if matchesRule(request, rule.Match) {
			return applyAction(request, clusters, rule.Action)
		}
	}
	// No matching rule -- fall back to first healthy cluster.
	for _, c := range clusters {
		if c.Healthy {
			return RouteDecision{
				TargetCluster: c.ClusterID,
				Reason:        "default-healthy",
			}, nil
		}
	}
	return RouteDecision{}, fmt.Errorf("no suitable cluster found")
}

// filterClustersByTenant restricts the candidate cluster list based on tenant
// cluster scope. If allowed is non-empty, only clusters in the allowed list
// are kept. Clusters in the denied list are always removed. When both lists
// are empty the full set is returned unchanged (backward compatible).
func filterClustersByTenant(clusters []ClusterHealth, allowed, denied []string) []ClusterHealth {
	if len(allowed) == 0 && len(denied) == 0 {
		return clusters
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	deniedSet := make(map[string]struct{}, len(denied))
	for _, id := range denied {
		deniedSet[id] = struct{}{}
	}

	filtered := make([]ClusterHealth, 0, len(clusters))
	for _, c := range clusters {
		if _, ok := deniedSet[c.ClusterID]; ok {
			continue
		}
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[c.ClusterID]; !ok {
				continue
			}
		}
		filtered = append(filtered, c)
	}
	return filtered
}

// matchesRule checks whether a request satisfies a routing rule's match
// criteria. An empty match matches all requests.
func matchesRule(request RoutingRequest, match v1alpha1.RoutingMatch) bool {
	if match.Source != "" && match.Source != request.SourceRegion {
		return false
	}
	for key, pattern := range match.Headers {
		value, ok := request.Headers[key]
		if !ok {
			return false
		}
		if pattern != "*" && pattern != value {
			return false
		}
	}
	if match.SemanticTier != "" {
		if request.SemanticLabel != match.SemanticTier {
			return false
		}
		if match.MinConfidence > 0 && request.SemanticMargin < match.MinConfidence {
			return false
		}
	}
	return true
}

// applyAction executes a routing action against the set of candidate clusters
// and returns a route decision.
func applyAction(request RoutingRequest, clusters []ClusterHealth, action v1alpha1.RoutingAction) (RouteDecision, error) {
	if action.PreferLocal {
		// Find local cluster by matching the source region prefix.
		var localCluster *ClusterHealth
		for i := range clusters {
			if strings.HasPrefix(clusters[i].ClusterID, request.SourceRegion) {
				localCluster = &clusters[i]
				break
			}
		}
		if localCluster != nil && localCluster.Healthy {
			return RouteDecision{
				TargetCluster: localCluster.ClusterID,
				Reason:        "prefer-local",
			}, nil
		}
		// Local cluster unavailable -- try failover targets.
		if action.Failover != nil {
			for _, failoverID := range action.Failover.Clusters {
				for i := range clusters {
					if clusters[i].ClusterID == failoverID && clusters[i].Healthy {
						return RouteDecision{
							TargetCluster: clusters[i].ClusterID,
							Reason:        "failover",
						}, nil
					}
				}
			}
		}
	}

	if action.KVCacheAffinity {
		var best *ClusterHealth
		for i := range clusters {
			if !clusters[i].Healthy {
				continue
			}
			if best == nil || clusters[i].KVCacheHitRate > best.KVCacheHitRate {
				best = &clusters[i]
			}
		}
		if best != nil {
			return RouteDecision{
				TargetCluster: best.ClusterID,
				Reason:        "kv-cache-affinity",
			}, nil
		}
	}

	if action.PreferCheapest {
		var best *ClusterHealth
		for i := range clusters {
			if !clusters[i].Healthy {
				continue
			}
			if action.MaxLatencyMs > 0 && clusters[i].LatencyMs > float64(action.MaxLatencyMs) {
				continue
			}
			if best == nil || clusters[i].CapacityRemaining > best.CapacityRemaining {
				best = &clusters[i]
			}
		}
		if best != nil {
			return RouteDecision{
				TargetCluster: best.ClusterID,
				Reason:        "prefer-cheapest",
			}, nil
		}
	}

	if action.TargetModelTier != "" {
		var best *ClusterHealth
		for i := range clusters {
			if !clusters[i].Healthy {
				continue
			}
			if best == nil || clusters[i].CapacityRemaining > best.CapacityRemaining {
				best = &clusters[i]
			}
		}
		if best != nil {
			headers := map[string]string{"X-Semantic-Label": request.SemanticLabel}
			return RouteDecision{
				TargetCluster:   best.ClusterID,
				HeadersToInject: headers,
				Reason:          "semantic-tier:" + action.TargetModelTier,
			}, nil
		}
	}

	// Fallback: first healthy cluster.
	for _, c := range clusters {
		if c.Healthy {
			return RouteDecision{
				TargetCluster: c.ClusterID,
				Reason:        "default",
			}, nil
		}
	}
	return RouteDecision{}, fmt.Errorf("no suitable cluster found for action")
}
