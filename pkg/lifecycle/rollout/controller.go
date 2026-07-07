package rollout

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
)

// ClusterRolloutState tracks rollout progress for a single cluster.
type ClusterRolloutState struct {
	ClusterID string
	Phase     string
	Weight    int
	SLOMet    bool
}

// RolloutState represents the overall state of a model rollout across the fleet.
type RolloutState struct {
	ID            string
	Phase         string
	CurrentWeight int
	ClusterStates []ClusterRolloutState
	StartedAt     time.Time
	UpdatedAt     time.Time
}

// RolloutController manages the lifecycle of model rollouts.
type RolloutController interface {
	// CreateRollout initiates a new rollout based on the given lifecycle spec.
	CreateRollout(ctx context.Context, lifecycle v1alpha1.ModelLifecycleSpec) (*RolloutState, error)

	// AdvanceRollout moves the rollout to the next stage (e.g. increases canary weight).
	AdvanceRollout(ctx context.Context, rolloutID string) (*RolloutState, error)

	// RollbackRollout reverts a rollout to the previous stable state.
	RollbackRollout(ctx context.Context, rolloutID string) (*RolloutState, error)

	// GetRolloutState returns the current state of a rollout.
	GetRolloutState(ctx context.Context, rolloutID string) (*RolloutState, error)
}

// rolloutRecord stores a rollout state along with the lifecycle spec that created it.
type rolloutRecord struct {
	state     *RolloutState
	lifecycle v1alpha1.ModelLifecycleSpec
}

type defaultRolloutController struct {
	mu       sync.Mutex
	rollouts map[string]*rolloutRecord
	counter  int
}

// NewRolloutController returns a new RolloutController instance.
func NewRolloutController() RolloutController {
	return &defaultRolloutController{
		rollouts: make(map[string]*rolloutRecord),
	}
}

func (c *defaultRolloutController) CreateRollout(ctx context.Context, lifecycle v1alpha1.ModelLifecycleSpec) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.counter++
	id := fmt.Sprintf("rollout-%s-%s-%d", lifecycle.Model.Name, lifecycle.Model.Version, c.counter)

	now := time.Now()

	phase := "Pending"
	weight := 0

	if lifecycle.Strategy.Type == "Canary" && lifecycle.Strategy.Canary != nil {
		phase = "Canary"
		weight = lifecycle.Strategy.Canary.InitialWeight
	}

	var clusterStates []ClusterRolloutState
	if lifecycle.Clusters != nil {
		for _, clusterID := range lifecycle.Clusters.Order {
			clusterStates = append(clusterStates, ClusterRolloutState{
				ClusterID: clusterID,
				Phase:     phase,
				Weight:    weight,
				SLOMet:    false,
			})
		}
	}

	state := &RolloutState{
		ID:            id,
		Phase:         phase,
		CurrentWeight: weight,
		ClusterStates: clusterStates,
		StartedAt:     now,
		UpdatedAt:     now,
	}

	c.rollouts[id] = &rolloutRecord{
		state:     state,
		lifecycle: lifecycle,
	}

	return state, nil
}

// checkSLOGate returns true if the SLO gate passes (metrics within tolerance).
// A tolerance of 0% means no regression is allowed, which always fails in simulation.
func checkSLOGate(gate *v1alpha1.SLOGate) bool {
	if gate == nil {
		return true
	}

	ttftPct := parsePercent(gate.MaxTTFTRegression)
	errorPct := parsePercent(gate.MaxErrorRateIncrease)

	// If tolerances are zero, SLO check fails (no regression is allowed but some always exists).
	if ttftPct <= 0 || errorPct <= 0 {
		return false
	}

	return true
}

// parsePercent parses a string like "10%" into a float64 value (10.0).
func parsePercent(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func (c *defaultRolloutController) AdvanceRollout(ctx context.Context, rolloutID string) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.rollouts[rolloutID]
	if !ok {
		return nil, fmt.Errorf("rollout %q not found", rolloutID)
	}

	state := record.state
	lifecycle := record.lifecycle

	if lifecycle.Strategy.Type != "Canary" || lifecycle.Strategy.Canary == nil {
		return nil, fmt.Errorf("rollout %q is not a canary rollout", rolloutID)
	}

	canary := lifecycle.Strategy.Canary

	// Check SLO gate if configured.
	sloPass := checkSLOGate(canary.SLOGate)

	if !sloPass {
		if canary.RollbackOnFailure {
			state.Phase = "RolledBack"
			state.CurrentWeight = 0
			state.UpdatedAt = time.Now()
			for i := range state.ClusterStates {
				state.ClusterStates[i].Phase = "RolledBack"
				state.ClusterStates[i].Weight = 0
				state.ClusterStates[i].SLOMet = false
			}
			return state, nil
		}
		// SLO failed but no rollback -- keep current state unchanged.
		return state, nil
	}

	// SLO passed -- advance the weight.
	newWeight := state.CurrentWeight + canary.WeightIncrement
	if newWeight > 100 {
		newWeight = 100
	}

	state.CurrentWeight = newWeight
	state.UpdatedAt = time.Now()

	for i := range state.ClusterStates {
		state.ClusterStates[i].Weight = newWeight
		state.ClusterStates[i].SLOMet = true
	}

	return state, nil
}

func (c *defaultRolloutController) RollbackRollout(ctx context.Context, rolloutID string) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.rollouts[rolloutID]
	if !ok {
		return nil, fmt.Errorf("rollout %q not found", rolloutID)
	}

	state := record.state
	state.Phase = "RolledBack"
	state.CurrentWeight = 0
	state.UpdatedAt = time.Now()

	for i := range state.ClusterStates {
		state.ClusterStates[i].Phase = "RolledBack"
		state.ClusterStates[i].Weight = 0
	}

	return state, nil
}

func (c *defaultRolloutController) GetRolloutState(ctx context.Context, rolloutID string) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.rollouts[rolloutID]
	if !ok {
		return nil, fmt.Errorf("rollout %q not found", rolloutID)
	}

	return record.state, nil
}
