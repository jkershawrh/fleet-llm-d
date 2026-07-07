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
// corresponding action to select a target cluster.
func (e *defaultRoutingPolicyEvaluator) Evaluate(ctx context.Context, request RoutingRequest, clusters []ClusterHealth, policy v1alpha1.FleetRoutingPolicySpec) (RouteDecision, error) {
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
