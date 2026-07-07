package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

// FleetPoolState tracks the reconciled state of a single fleet inference pool.
type FleetPoolState struct {
	Name            string
	Model           string
	Source          string
	DesiredClusters []string
	ActualClusters  []string
	Phase           v1alpha1.FleetPhase
	LastReconciled  time.Time
}

// Reconciler drives the reconciliation loop for fleet inference pools.
type Reconciler struct {
	mu       sync.RWMutex
	pools    map[string]*FleetPoolState
	solver   solver.ConstraintSolver
	clusters func(ctx context.Context) ([]solver.ClusterInfo, error) // function to list available clusters
	onChange func(pool *FleetPoolState)                               // callback when state changes
}

// NewReconciler creates a Reconciler with the given constraint solver and
// cluster listing function.
func NewReconciler(s solver.ConstraintSolver, clusterLister func(ctx context.Context) ([]solver.ClusterInfo, error)) *Reconciler {
	return &Reconciler{
		pools:    make(map[string]*FleetPoolState),
		solver:   s,
		clusters: clusterLister,
	}
}

// SetOnChange registers a callback that is invoked whenever a pool's state
// changes during reconciliation.
func (r *Reconciler) SetOnChange(fn func(pool *FleetPoolState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

// ReconcilePool reconciles the desired state described by pool against the
// available clusters. It updates the internal pool state and transitions the
// phase through Pending -> Placing -> Running (or Failed on error).
func (r *Reconciler) ReconcilePool(ctx context.Context, pool v1alpha1.FleetInferencePoolSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := pool.Model.Name

	// Set initial state to Pending if new, or keep existing phase.
	state, exists := r.pools[name]
	if !exists {
		state = &FleetPoolState{
			Name:   name,
			Model:  pool.Model.Name,
			Source: pool.Model.Source,
			Phase:  v1alpha1.FleetPhasePending,
		}
		r.pools[name] = state
	}

	// Transition to Placing.
	state.Phase = v1alpha1.FleetPhasePlacing

	// Get available clusters.
	clusters, err := r.clusters(ctx)
	if err != nil {
		state.Phase = v1alpha1.FleetPhaseFailed
		return fmt.Errorf("listing clusters: %w", err)
	}

	// Run constraint solver with a default (empty) placement policy.
	// The real policy would be looked up by pool.Placement.PolicyRef.
	policy := v1alpha1.PlacementPolicySpec{}
	decisions, err := r.solver.Solve(ctx, pool, clusters, policy)
	if err != nil {
		state.Phase = v1alpha1.FleetPhaseFailed
		return fmt.Errorf("solving placement: %w", err)
	}

	// Extract cluster IDs from placement decisions.
	desired := make([]string, 0, len(decisions))
	for _, d := range decisions {
		desired = append(desired, d.ClusterID)
	}
	state.DesiredClusters = desired

	// Transition to Running.
	state.Phase = v1alpha1.FleetPhaseRunning
	state.LastReconciled = time.Now()

	// Invoke onChange callback if set.
	if r.onChange != nil {
		r.onChange(state)
	}

	return nil
}

// DeletePool removes the named pool from the reconciler's state. It returns
// an error if the pool does not exist.
func (r *Reconciler) DeletePool(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.pools[name]; !ok {
		return fmt.Errorf("pool %q not found", name)
	}
	delete(r.pools, name)
	return nil
}

// GetPoolState returns a copy of the current state for the named pool.
// It returns an error if the pool does not exist.
func (r *Reconciler) GetPoolState(name string) (*FleetPoolState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.pools[name]
	if !ok {
		return nil, fmt.Errorf("pool %q not found", name)
	}
	// Return a copy to avoid data races.
	cp := *state
	cp.DesiredClusters = make([]string, len(state.DesiredClusters))
	copy(cp.DesiredClusters, state.DesiredClusters)
	cp.ActualClusters = make([]string, len(state.ActualClusters))
	copy(cp.ActualClusters, state.ActualClusters)
	return &cp, nil
}

// ListPools returns a snapshot of all pool states. The returned values are
// copies, not pointers, so they are safe to use without holding the lock.
func (r *Reconciler) ListPools() []FleetPoolState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pools := make([]FleetPoolState, 0, len(r.pools))
	for _, state := range r.pools {
		cp := *state
		cp.DesiredClusters = make([]string, len(state.DesiredClusters))
		copy(cp.DesiredClusters, state.DesiredClusters)
		cp.ActualClusters = make([]string, len(state.ActualClusters))
		copy(cp.ActualClusters, state.ActualClusters)
		pools = append(pools, cp)
	}
	return pools
}

// watchEvent is the JSON structure expected by the WatchEndpoint handler.
type watchEvent struct {
	Type   string                          `json:"type"`
	Object v1alpha1.FleetInferencePoolSpec `json:"object"`
}

// WatchEndpoint returns an http.HandlerFunc that accepts POST requests
// containing watch events and reconciles them accordingly.
func (r *Reconciler) WatchEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var event watchEvent
		if err := json.NewDecoder(req.Body).Decode(&event); err != nil {
			http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
			return
		}

		log.Printf("watch event: type=%s model=%s", event.Type, event.Object.Model.Name)

		switch event.Type {
		case "ADDED", "MODIFIED":
			if err := r.ReconcilePool(req.Context(), event.Object); err != nil {
				http.Error(w, fmt.Sprintf("reconcile error: %v", err), http.StatusInternalServerError)
				return
			}
		case "DELETED":
			if err := r.DeletePool(req.Context(), event.Object.Model.Name); err != nil {
				http.Error(w, fmt.Sprintf("delete error: %v", err), http.StatusInternalServerError)
				return
			}
		default:
			http.Error(w, fmt.Sprintf("unknown event type: %s", event.Type), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
