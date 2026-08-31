//go:build bdd

package steps

import (
	"encoding/json"
	"fmt"
)

// RecordPlacementDecision records a placement decision in the ledger.
func (w *World) RecordPlacementDecision(model, cluster string, replicas int, gpuType, reason string) error {
	receipt, err := w.Recorder.RecordPlacement(w.Ctx, model, cluster, replicas, gpuType, reason)
	if err != nil {
		w.LastError = err
		return nil
	}
	w.LedgerEntries = append(w.LedgerEntries, LedgerEntry{
		Type:    "fleet.placement.assigned",
		Receipt: receipt,
		Content: map[string]interface{}{
			"model":    model,
			"cluster":  cluster,
			"replicas": replicas,
			"gpuType":  gpuType,
			"reason":   reason,
		},
	})
	return nil
}

// RecordScalingDecision records a scaling decision in the ledger.
func (w *World) RecordScalingDecision(cluster, pool string, fromReplicas, toReplicas int, reason string) error {
	receipt, err := w.Recorder.RecordScalingEvent(w.Ctx, cluster, pool, fromReplicas, toReplicas, reason)
	if err != nil {
		w.LastError = err
		return nil
	}
	w.LedgerEntries = append(w.LedgerEntries, LedgerEntry{
		Type:    "fleet.scaling.adjusted",
		Receipt: receipt,
		Content: map[string]interface{}{
			"cluster":      cluster,
			"pool":         pool,
			"fromReplicas": fromReplicas,
			"toReplicas":   toReplicas,
			"reason":       reason,
		},
	})
	return nil
}

// RecordRoutingDecision records a routing decision in the ledger.
func (w *World) RecordRoutingDecision(model, fromCluster, toCluster string, weightDelta float64, reason string) error {
	receipt, err := w.Recorder.RecordRoutingChange(w.Ctx, model, fromCluster, toCluster, weightDelta, reason)
	if err != nil {
		w.LastError = err
		return nil
	}
	w.LedgerEntries = append(w.LedgerEntries, LedgerEntry{
		Type:    "fleet.routing.shifted",
		Receipt: receipt,
		Content: map[string]interface{}{
			"model":       model,
			"fromCluster": fromCluster,
			"toCluster":   toCluster,
			"weightDelta": weightDelta,
			"reason":      reason,
		},
	})
	return nil
}

// VerifyAllChains verifies all decision chains in the ledger.
func (w *World) VerifyAllChains() (map[string]bool, error) {
	verifications, err := w.Recorder.VerifyAllChains(w.Ctx)
	if err != nil {
		return nil, fmt.Errorf("chain verification failed: %w", err)
	}

	results := make(map[string]bool)
	for chainType, v := range verifications {
		results[chainType] = v.Valid
	}
	return results, nil
}

// AssertLedgerEntryHasFields checks that a ledger entry contains specific fields.
func (w *World) AssertLedgerEntryHasFields(entryType string, fields map[string]interface{}) error {
	for _, entry := range w.LedgerEntries {
		if entry.Type == entryType && entry.Content != nil {
			for k, v := range fields {
				actual, ok := entry.Content[k]
				if !ok {
					return fmt.Errorf("ledger entry %q missing field %q", entryType, k)
				}
				if fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", v) {
					return fmt.Errorf("ledger entry %q field %q: expected %v, got %v", entryType, k, v, actual)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("no ledger entry of type %q found", entryType)
}

// AssertLedgerEntriesOrdered checks that entries have increasing chain positions.
func (w *World) AssertLedgerEntriesOrdered() error {
	var lastPos int64 = -1
	for i, entry := range w.LedgerEntries {
		if entry.Receipt != nil {
			if entry.Receipt.ChainPosition <= lastPos && lastPos >= 0 {
				return fmt.Errorf("entry %d chain position %d not after previous %d", i, entry.Receipt.ChainPosition, lastPos)
			}
			lastPos = entry.Receipt.ChainPosition
		}
	}
	return nil
}

// AssertLedgerEntryHasTimestamp checks that a ledger entry has a non-zero timestamp.
func (w *World) AssertLedgerEntryHasTimestamp(entryType string) error {
	for _, entry := range w.LedgerEntries {
		if entry.Type == entryType && entry.Receipt != nil {
			if entry.Receipt.Timestamp.IsZero() {
				return fmt.Errorf("ledger entry %q has zero timestamp", entryType)
			}
			return nil
		}
	}
	return fmt.Errorf("no ledger entry of type %q with receipt found", entryType)
}

// SerializeLedgerEntry serializes a ledger entry content to JSON.
func SerializeLedgerEntry(content map[string]interface{}) ([]byte, error) {
	return json.Marshal(content)
}

// AssertLedgerEntryCount checks the total number of ledger entries.
func (w *World) AssertLedgerEntryCount(expected int) error {
	if len(w.LedgerEntries) != expected {
		return fmt.Errorf("expected %d ledger entries, got %d", expected, len(w.LedgerEntries))
	}
	return nil
}
