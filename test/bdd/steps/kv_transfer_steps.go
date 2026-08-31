//go:build bdd

package steps

import (
	"fmt"

	"github.com/llm-d/fleet-llm-d/pkg/kvcache/transfer"
)

// InitiateKVTransfer starts a KV cache transfer between clusters.
func (w *World) InitiateKVTransfer(source, target, model, transferType string, maxBandwidth int) error {
	req := transfer.TransferRequest{
		SourceCluster:    source,
		TargetCluster:    target,
		Model:            model,
		TransferType:     transferType,
		MaxBandwidthMbps: maxBandwidth,
	}

	job, err := w.Orchestrator.InitiateTransfer(w.Ctx, req)
	if err != nil {
		w.LastError = err
		return nil
	}

	w.LastTransfer = &TransferResult{Job: job}
	w.LastError = nil
	return nil
}

// GetTransferStatus retrieves the current status of a transfer job.
func (w *World) GetTransferStatus(jobID string) error {
	job, err := w.Orchestrator.GetTransferStatus(w.Ctx, jobID)
	if err != nil {
		w.LastError = err
		return nil
	}
	w.LastTransfer = &TransferResult{Job: job}
	return nil
}

// CancelTransfer cancels a running transfer.
func (w *World) CancelTransfer(jobID string) error {
	err := w.Orchestrator.CancelTransfer(w.Ctx, jobID)
	if err != nil {
		w.LastError = err
	}
	return nil
}

// AssertTransferPhase checks the transfer job phase.
func (w *World) AssertTransferPhase(expected string) error {
	if w.LastTransfer == nil || w.LastTransfer.Job == nil {
		return fmt.Errorf("no transfer job available")
	}
	if w.LastTransfer.Job.Phase != expected {
		return fmt.Errorf("transfer phase is %q, expected %q", w.LastTransfer.Job.Phase, expected)
	}
	return nil
}

// AssertTransferInitiated checks that a transfer was initiated.
func (w *World) AssertTransferInitiated() error {
	if w.LastTransfer == nil || w.LastTransfer.Job == nil {
		return fmt.Errorf("no transfer was initiated")
	}
	if w.LastTransfer.Job.ID == "" {
		return fmt.Errorf("transfer job has no ID")
	}
	return nil
}

// RecordKVTransferLedger records a KV cache transfer in the ledger.
func (w *World) RecordKVTransferLedger(source, target, model string, bytesTransferred int64) error {
	cacheHash := fmt.Sprintf("sha256:%s-%s-%d", source, target, bytesTransferred)
	proof, err := w.Recorder.RecordKVCacheTransfer(w.Ctx, source, target, model, bytesTransferred, cacheHash)
	if err != nil {
		w.LastError = err
		return nil
	}
	w.LedgerEntries = append(w.LedgerEntries, LedgerEntry{
		Type:         "fleet.kvcache.transferred",
		ProofReceipt: proof,
	})
	return nil
}

// AssertProofReceiptValid checks that a proof receipt was issued.
func (w *World) AssertProofReceiptValid() error {
	for _, entry := range w.LedgerEntries {
		if entry.ProofReceipt != nil && entry.Type == "fleet.kvcache.transferred" {
			if entry.ProofReceipt.EntryHash == "" {
				return fmt.Errorf("proof receipt has empty hash")
			}
			return nil
		}
	}
	return fmt.Errorf("no KV cache transfer proof receipt found")
}

// EstimateTransferDuration estimates transfer time in seconds at a given bandwidth.
func EstimateTransferDuration(dataSizeBytes int64, bandwidthMbps int) float64 {
	if bandwidthMbps == 0 {
		return 0
	}
	dataSizeMbits := float64(dataSizeBytes) * 8 / 1_000_000
	return dataSizeMbits / float64(bandwidthMbps)
}

// ComputePerTransferBandwidth calculates bandwidth per transfer when concurrent.
func ComputePerTransferBandwidth(totalBandwidth, concurrentTransfers int) int {
	if concurrentTransfers == 0 {
		return totalBandwidth
	}
	return totalBandwidth / concurrentTransfers
}
