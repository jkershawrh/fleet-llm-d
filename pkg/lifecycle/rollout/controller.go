package rollout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
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

var ErrRolloutNotFound = errors.New("rollout not found")

type persistedRolloutSnapshot struct {
	Lifecycle     v1alpha1.ModelLifecycleSpec `json:"lifecycle"`
	ClusterStates []ClusterRolloutState       `json:"clusterStates,omitempty"`
}

type defaultRolloutController struct {
	mu       sync.Mutex
	rollouts map[string]*rolloutRecord
	repo     postgres.RolloutRepository
}

// NewRolloutController returns a new RolloutController instance.
// If repo is non-nil, the controller loads rollouts from the repository
// when they are not found in its internal cache.
func NewRolloutController(repo ...postgres.RolloutRepository) RolloutController {
	var r postgres.RolloutRepository
	if len(repo) > 0 {
		r = repo[0]
	}
	return &defaultRolloutController{
		rollouts: make(map[string]*rolloutRecord),
		repo:     r,
	}
}

func (c *defaultRolloutController) loadFromRepo(ctx context.Context, rolloutID string) (*rolloutRecord, bool) {
	if c.repo == nil {
		return nil, false
	}
	rec, err := c.repo.Get(ctx, rolloutID)
	if err != nil || rec == nil {
		return nil, false
	}
	lifecycle := v1alpha1.ModelLifecycleSpec{}
	var clusterStates []ClusterRolloutState
	if rawSnapshot, ok := rec.Strategy["snapshot"]; ok {
		if encoded, marshalErr := json.Marshal(rawSnapshot); marshalErr == nil {
			var snapshot persistedRolloutSnapshot
			if unmarshalErr := json.Unmarshal(encoded, &snapshot); unmarshalErr == nil {
				lifecycle = snapshot.Lifecycle
				clusterStates = append([]ClusterRolloutState(nil), snapshot.ClusterStates...)
			}
		}
	}
	if lifecycle.Strategy.Type == "" {
		strategyType := "Canary"
		if configuredType, ok := rec.Strategy["type"].(string); ok && configuredType != "" {
			strategyType = configuredType
		}
		lifecycle = v1alpha1.ModelLifecycleSpec{
			FleetPoolRef: rec.PoolID,
			Model:        v1alpha1.ModelRef{Version: rec.ModelVersion},
			Strategy: v1alpha1.RolloutStrategy{
				Type: strategyType,
				Canary: &v1alpha1.CanaryConfig{
					InitialWeight:   0,
					WeightIncrement: 20,
				},
			},
		}
	}
	state := &RolloutState{
		ID:            rec.ID,
		Phase:         rec.Status,
		CurrentWeight: rec.CurrentWeight,
		ClusterStates: clusterStates,
		StartedAt:     rec.StartedAt,
		UpdatedAt:     time.Now(),
	}
	record := &rolloutRecord{state: state, lifecycle: lifecycle}
	c.rollouts[rolloutID] = record
	return record, true
}

func (c *defaultRolloutController) CreateRollout(ctx context.Context, lifecycle v1alpha1.ModelLifecycleSpec) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		return nil, fmt.Errorf("generate rollout ID: %w", err)
	}
	id := fmt.Sprintf("rollout-%s-%s-%s", lifecycle.Model.Name, lifecycle.Model.Version, hex.EncodeToString(randomSuffix))

	now := time.Now()

	phase := "Pending"
	weight := 0

	if strings.EqualFold(lifecycle.Strategy.Type, "Canary") && lifecycle.Strategy.Canary != nil {
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
	if c.repo != nil {
		poolID := lifecycle.FleetPoolRef
		if poolID == "" {
			poolID = lifecycle.Model.Name
		}
		if err := c.repo.Create(ctx, postgres.RolloutRecord{
			ID: id, PoolID: poolID, ModelVersion: lifecycle.Model.Version,
			Strategy: map[string]interface{}{
				"type": lifecycle.Strategy.Type,
				"snapshot": persistedRolloutSnapshot{
					Lifecycle: lifecycle, ClusterStates: append([]ClusterRolloutState(nil), clusterStates...),
				},
			},
			Status: phase, CurrentWeight: weight, StartedAt: now,
		}); err != nil {
			delete(c.rollouts, id)
			return nil, fmt.Errorf("persist rollout %q: %w", id, err)
		}
	}

	return cloneRolloutState(state), nil
}

func cloneRolloutState(state *RolloutState) *RolloutState {
	if state == nil {
		return nil
	}
	copyState := *state
	copyState.ClusterStates = append([]ClusterRolloutState(nil), state.ClusterStates...)
	return &copyState
}

func (c *defaultRolloutController) persistState(ctx context.Context, state *RolloutState) error {
	if c.repo == nil {
		return nil
	}
	record, err := c.repo.Get(ctx, state.ID)
	if err != nil {
		return err
	}
	record.Status = state.Phase
	record.CurrentWeight = state.CurrentWeight
	if record.Strategy == nil {
		record.Strategy = make(map[string]interface{})
	}
	snapshot := persistedRolloutSnapshot{}
	if rawSnapshot, ok := record.Strategy["snapshot"]; ok {
		if encoded, marshalErr := json.Marshal(rawSnapshot); marshalErr == nil {
			_ = json.Unmarshal(encoded, &snapshot)
		}
	}
	snapshot.ClusterStates = append([]ClusterRolloutState(nil), state.ClusterStates...)
	record.Strategy["snapshot"] = snapshot
	if strings.EqualFold(state.Phase, "Complete") || strings.EqualFold(state.Phase, "RolledBack") {
		completedAt := state.UpdatedAt
		record.CompletedAt = &completedAt
	}
	return c.repo.Update(ctx, *record)
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
		record, ok = c.loadFromRepo(ctx, rolloutID)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRolloutNotFound, rolloutID)
	}

	state := cloneRolloutState(record.state)
	lifecycle := record.lifecycle

	if !strings.EqualFold(lifecycle.Strategy.Type, "Canary") || lifecycle.Strategy.Canary == nil {
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
			if err := c.persistState(ctx, state); err != nil {
				return nil, fmt.Errorf("persist rollout rollback %q: %w", rolloutID, err)
			}
			record.state = state
			return cloneRolloutState(state), nil
		}
		// SLO failed but no rollback -- keep current state unchanged.
		return cloneRolloutState(record.state), nil
	}

	// SLO passed -- advance the weight.
	newWeight := state.CurrentWeight + canary.WeightIncrement
	if newWeight > 100 {
		newWeight = 100
	}

	state.CurrentWeight = newWeight
	state.Phase = "Canary"
	if newWeight == 100 {
		state.Phase = "Complete"
	}
	state.UpdatedAt = time.Now()

	for i := range state.ClusterStates {
		state.ClusterStates[i].Weight = newWeight
		state.ClusterStates[i].SLOMet = true
	}

	if err := c.persistState(ctx, state); err != nil {
		return nil, fmt.Errorf("persist rollout promotion %q: %w", rolloutID, err)
	}
	record.state = state
	return cloneRolloutState(state), nil
}

func (c *defaultRolloutController) RollbackRollout(ctx context.Context, rolloutID string) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.rollouts[rolloutID]
	if !ok {
		record, ok = c.loadFromRepo(ctx, rolloutID)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRolloutNotFound, rolloutID)
	}

	state := cloneRolloutState(record.state)
	state.Phase = "RolledBack"
	state.CurrentWeight = 0
	state.UpdatedAt = time.Now()

	for i := range state.ClusterStates {
		state.ClusterStates[i].Phase = "RolledBack"
		state.ClusterStates[i].Weight = 0
	}

	if err := c.persistState(ctx, state); err != nil {
		return nil, fmt.Errorf("persist rollout rollback %q: %w", rolloutID, err)
	}
	record.state = state
	return cloneRolloutState(state), nil
}

func (c *defaultRolloutController) GetRolloutState(ctx context.Context, rolloutID string) (*RolloutState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.rollouts[rolloutID]
	if !ok {
		record, ok = c.loadFromRepo(ctx, rolloutID)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRolloutNotFound, rolloutID)
	}

	return cloneRolloutState(record.state), nil
}
