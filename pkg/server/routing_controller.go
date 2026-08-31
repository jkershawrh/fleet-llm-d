package server

import (
	"context"
	"sync"

	"github.com/llm-d/fleet-llm-d/pkg/classifier"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

// RoutingController owns the session-affinity, semantic classification, and
// tier-escalation state that the routing handler depends on. Extracting it
// from FleetController is the first step toward decomposing the 39-field
// god struct.
type RoutingController struct {
	SessionTable     *routing.SessionAffinityTable
	ClassifierClient classifier.ClassifierClient
	ClassifierCache  *classifier.ClassificationCache
	Evaluator        policy.RoutingPolicyEvaluator

	sessionTierMu sync.RWMutex
	sessionTiers  map[string]string
}

func NewRoutingController() *RoutingController {
	return &RoutingController{
		Evaluator: policy.NewRoutingPolicyEvaluator(),
	}
}

func (rc *RoutingController) SessionTierRank(sessionID string) int {
	rc.sessionTierMu.RLock()
	defer rc.sessionTierMu.RUnlock()
	if tier, ok := rc.sessionTiers[sessionID]; ok {
		return tierRank(tier)
	}
	return -1
}

func (rc *RoutingController) SetSessionTier(sessionID, tier string) {
	rc.sessionTierMu.Lock()
	defer rc.sessionTierMu.Unlock()
	if rc.sessionTiers == nil {
		rc.sessionTiers = make(map[string]string)
	}
	rc.sessionTiers[sessionID] = tier
}

func (rc *RoutingController) SemanticTierDistribution() map[string]float64 {
	if rc.ClassifierCache == nil {
		return nil
	}
	return rc.ClassifierCache.TierDistribution()
}

func (rc *RoutingController) ClassifyPrompt(ctx context.Context, text, requestID string) (*classifier.ClassifyResult, error) {
	if rc.ClassifierClient == nil {
		return nil, nil
	}
	if cached, ok := rc.ClassifierCache.Get(text); ok {
		return cached, nil
	}
	result, err := rc.ClassifierClient.Classify(ctx, text, requestID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	rc.ClassifierCache.Put(text, result)
	return result, nil
}
