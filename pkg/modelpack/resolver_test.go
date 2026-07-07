package modelpack

import (
	"context"
	"strings"
	"testing"
)

func TestResolve_ValidRef(t *testing.T) {
	resolver := NewRegistryModelResolver()
	ref := "registry.example.com/models/llama-3-70b:v1"

	_, err := resolver.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error from stub resolver, got nil")
	}

	// The stub should return "not implemented", not a validation error.
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("expected 'not implemented' error, got: %v", err)
	}
}

func TestResolve_NotFound(t *testing.T) {
	resolver := NewRegistryModelResolver()
	ref := "registry.example.com/models/nonexistent-model:latest"

	_, err := resolver.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error for non-existent model, got nil")
	}

	// Stub returns "not implemented" for any valid ref.
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("expected 'not implemented' error, got: %v", err)
	}
}

func TestResolve_InvalidRef(t *testing.T) {
	resolver := NewRegistryModelResolver()

	tests := []struct {
		name    string
		ref     string
		wantErr string
	}{
		{
			name:    "empty reference",
			ref:     "",
			wantErr: "empty reference",
		},
		{
			name:    "no repository path",
			ref:     "justahostname",
			wantErr: "missing repository path",
		},
		{
			name:    "invalid host without dot",
			ref:     "notahost/repo",
			wantErr: "invalid registry host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolver.Resolve(context.Background(), tt.ref)
			if err == nil {
				t.Fatal("expected error for invalid ref, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}
