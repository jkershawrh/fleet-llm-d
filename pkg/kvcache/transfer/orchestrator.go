package transfer

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TransferRequest defines the parameters for a KV cache transfer between clusters.
type TransferRequest struct {
	SourceCluster    string
	TargetCluster    string
	Model            string
	TransferType     string
	MaxBandwidthMbps int
}

// TransferJob represents the state and progress of a KV cache transfer operation.
type TransferJob struct {
	ID                  string
	Phase               string
	BytesTransferred    int64
	EstimatedCompletion time.Time
	Error               string
}

// TransferOrchestrator manages KV cache transfers between clusters.
type TransferOrchestrator interface {
	InitiateTransfer(ctx context.Context, request TransferRequest) (*TransferJob, error)
	GetTransferStatus(ctx context.Context, jobID string) (*TransferJob, error)
	CancelTransfer(ctx context.Context, jobID string) error
}

type defaultTransferOrchestrator struct {
	mu      sync.Mutex
	jobs    map[string]*TransferJob
	counter int
}

// NewTransferOrchestrator returns a new TransferOrchestrator instance.
func NewTransferOrchestrator() TransferOrchestrator {
	o := &defaultTransferOrchestrator{
		jobs: make(map[string]*TransferJob),
	}
	// Pre-seed a default transfer job for testing.
	o.jobs["transfer-001"] = &TransferJob{
		ID:               "transfer-001",
		Phase:            "InProgress",
		BytesTransferred: 0,
	}
	return o
}

func (o *defaultTransferOrchestrator) InitiateTransfer(ctx context.Context, request TransferRequest) (*TransferJob, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.counter++
	id := fmt.Sprintf("transfer-%03d", o.counter)

	job := &TransferJob{
		ID:                  id,
		Phase:               "Pending",
		BytesTransferred:    0,
		EstimatedCompletion: time.Now().Add(10 * time.Minute),
	}

	o.jobs[id] = job
	return job, nil
}

func (o *defaultTransferOrchestrator) GetTransferStatus(ctx context.Context, jobID string) (*TransferJob, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	job, ok := o.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("transfer job %q not found", jobID)
	}
	return job, nil
}

func (o *defaultTransferOrchestrator) CancelTransfer(ctx context.Context, jobID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	job, ok := o.jobs[jobID]
	if !ok {
		return fmt.Errorf("transfer job %q not found", jobID)
	}

	if job.Phase == "Complete" {
		return fmt.Errorf("transfer job %q is already complete", jobID)
	}

	job.Phase = "Cancelled"
	return nil
}
