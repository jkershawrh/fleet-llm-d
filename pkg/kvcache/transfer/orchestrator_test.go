package transfer

import (
	"context"
	"testing"
)

func TestInitiateTransfer(t *testing.T) {
	tests := []struct {
		name    string
		request TransferRequest
		wantPhase string
		wantErr bool
	}{
		{
			name: "initiate transfer between clusters",
			request: TransferRequest{
				SourceCluster:    "cluster-a",
				TargetCluster:    "cluster-b",
				Model:            "llama-3-70b",
				TransferType:     "full",
				MaxBandwidthMbps: 1000,
			},
			wantPhase: "Pending",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator := NewTransferOrchestrator()
			job, err := orchestrator.InitiateTransfer(context.Background(), tt.request)
			if (err != nil) != tt.wantErr {
				t.Errorf("InitiateTransfer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if job == nil {
				t.Fatal("InitiateTransfer() returned nil job")
			}
			if job.Phase != tt.wantPhase {
				t.Errorf("InitiateTransfer() phase = %v, want %v", job.Phase, tt.wantPhase)
			}
		})
	}
}

func TestGetTransferStatus(t *testing.T) {
	tests := []struct {
		name    string
		jobID   string
		wantErr bool
	}{
		{
			name:    "get status of active transfer job",
			jobID:   "transfer-001",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator := NewTransferOrchestrator()
			job, err := orchestrator.GetTransferStatus(context.Background(), tt.jobID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTransferStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if job == nil {
				t.Fatal("GetTransferStatus() returned nil job")
			}
			if job.ID != tt.jobID {
				t.Errorf("GetTransferStatus() job ID = %v, want %v", job.ID, tt.jobID)
			}
			if job.BytesTransferred < 0 {
				t.Errorf("GetTransferStatus() BytesTransferred = %v, want >= 0", job.BytesTransferred)
			}
		})
	}
}

func TestCancelTransfer(t *testing.T) {
	tests := []struct {
		name    string
		jobID   string
		wantErr bool
	}{
		{
			name:    "cancel active transfer",
			jobID:   "transfer-001",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator := NewTransferOrchestrator()
			err := orchestrator.CancelTransfer(context.Background(), tt.jobID)
			if (err != nil) != tt.wantErr {
				t.Errorf("CancelTransfer() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
