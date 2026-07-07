//go:build bdd

package steps

import (
	"fmt"
	"strings"

	v1alpha1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1alpha1"
	"github.com/llm-d/fleet-llm-d/pkg/lifecycle/rollout"
)

// CreateCanaryRollout creates a canary rollout for a model version.
func (w *World) CreateCanaryRollout(name, model, version string, initialWeight, weightIncrement int,
	sloMaxTTFT, sloMaxErrorRate string, rollbackOnFailure bool) error {

	lifecycle := v1alpha1.ModelLifecycleSpec{
		Model: v1alpha1.ModelRef{
			Name:    model,
			Version: version,
		},
		FleetPoolRef: "default-pool",
		Strategy: v1alpha1.RolloutStrategy{
			Type: "Canary",
			Canary: &v1alpha1.CanaryConfig{
				InitialWeight:   initialWeight,
				WeightIncrement: weightIncrement,
				Interval:        "15m",
				SLOGate: &v1alpha1.SLOGate{
					MaxTTFTRegression:    sloMaxTTFT,
					MaxErrorRateIncrease: sloMaxErrorRate,
				},
				RollbackOnFailure: rollbackOnFailure,
			},
		},
	}

	state, err := w.Rollout.CreateRollout(w.Ctx, lifecycle)
	if err != nil {
		return fmt.Errorf("failed to create rollout: %w", err)
	}

	w.Rollouts[name] = &RolloutTestState{
		Lifecycle: lifecycle,
		State:     state,
	}
	return nil
}

// CreateRollingRollout creates a rolling (staged) rollout.
func (w *World) CreateRollingRollout(name, model, version string, clusterOrder []string) error {
	lifecycle := v1alpha1.ModelLifecycleSpec{
		Model: v1alpha1.ModelRef{
			Name:    model,
			Version: version,
		},
		FleetPoolRef: "default-pool",
		Strategy: v1alpha1.RolloutStrategy{
			Type: "Rolling",
		},
		Clusters: &v1alpha1.ClusterOrder{
			Order: clusterOrder,
		},
	}

	state, err := w.Rollout.CreateRollout(w.Ctx, lifecycle)
	if err != nil {
		return fmt.Errorf("failed to create rolling rollout: %w", err)
	}

	w.Rollouts[name] = &RolloutTestState{
		Lifecycle: lifecycle,
		State:     state,
	}
	return nil
}

// CreateBlueGreenRollout creates a blue-green rollout.
func (w *World) CreateBlueGreenRollout(name, model, version string) error {
	lifecycle := v1alpha1.ModelLifecycleSpec{
		Model: v1alpha1.ModelRef{
			Name:    model,
			Version: version,
		},
		FleetPoolRef: "default-pool",
		Strategy: v1alpha1.RolloutStrategy{
			Type: "BlueGreen",
		},
	}

	state, err := w.Rollout.CreateRollout(w.Ctx, lifecycle)
	if err != nil {
		return fmt.Errorf("failed to create blue-green rollout: %w", err)
	}

	w.Rollouts[name] = &RolloutTestState{
		Lifecycle: lifecycle,
		State:     state,
	}
	return nil
}

// AdvanceRollout advances a rollout by one step.
func (w *World) AdvanceRollout(name string) error {
	rs, ok := w.Rollouts[name]
	if !ok {
		return fmt.Errorf("rollout %q not found", name)
	}

	state, err := w.Rollout.AdvanceRollout(w.Ctx, rs.State.ID)
	if err != nil {
		w.LastError = err
		// Update state even on error - the controller may have changed it
		if state != nil {
			rs.State = state
		}
		return nil
	}

	rs.State = state
	w.LastError = nil
	return nil
}

// RollbackRollout triggers a rollback.
func (w *World) RollbackRollout(name string) error {
	rs, ok := w.Rollouts[name]
	if !ok {
		return fmt.Errorf("rollout %q not found", name)
	}

	state, err := w.Rollout.RollbackRollout(w.Ctx, rs.State.ID)
	if err != nil {
		return fmt.Errorf("failed to rollback: %w", err)
	}

	rs.State = state
	return nil
}

// AssertRolloutPhase checks the phase of a rollout.
func (w *World) AssertRolloutPhase(name, expectedPhase string) error {
	rs, ok := w.Rollouts[name]
	if !ok {
		return fmt.Errorf("rollout %q not found", name)
	}
	if !strings.EqualFold(rs.State.Phase, expectedPhase) {
		return fmt.Errorf("rollout %q phase is %q, expected %q", name, rs.State.Phase, expectedPhase)
	}
	return nil
}

// AssertCanaryWeight checks the current weight of a canary rollout.
func (w *World) AssertCanaryWeight(name string, expected int) error {
	rs, ok := w.Rollouts[name]
	if !ok {
		return fmt.Errorf("rollout %q not found", name)
	}
	if rs.State.CurrentWeight != expected {
		return fmt.Errorf("rollout %q weight is %d, expected %d", name, rs.State.CurrentWeight, expected)
	}
	return nil
}

// AssertRolloutRolledBack checks that a rollout has been rolled back.
func (w *World) AssertRolloutRolledBack(name string) error {
	return w.AssertRolloutPhase(name, "RolledBack")
}

// AssertRolloutComplete checks that a rollout is complete.
func (w *World) AssertRolloutComplete(name string) error {
	return w.AssertRolloutPhase(name, "Complete")
}

// GetRolloutState retrieves the current state of a rollout.
func (w *World) GetRolloutState(name string) (*rollout.RolloutState, error) {
	rs, ok := w.Rollouts[name]
	if !ok {
		return nil, fmt.Errorf("rollout %q not found", name)
	}

	state, err := w.Rollout.GetRolloutState(w.Ctx, rs.State.ID)
	if err != nil {
		return nil, err
	}
	rs.State = state
	return state, nil
}

// RecordDeploymentLedger records a model deployment in the ledger.
func (w *World) RecordDeploymentLedger(model, cluster string) error {
	receipt, err := w.Recorder.RecordLifecycleEvent(w.Ctx, model, "v1.0", "deployed", cluster, nil)
	if err != nil {
		w.LastError = err
		return nil
	}
	w.LedgerEntries = append(w.LedgerEntries, LedgerEntry{
		Type:    "fleet.lifecycle.deployed",
		Receipt: receipt,
	})
	return nil
}
