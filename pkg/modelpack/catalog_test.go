package modelpack

import (
	"context"
	"strings"
	"testing"
)

func TestRegister(t *testing.T) {
	catalog := NewInMemoryModelCatalog(nil)
	ctx := context.Background()

	config, err := catalog.Register(ctx, "registry.example.com/models/llama-3-70b:v1")
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if config == nil {
		t.Fatal("Register() returned nil config")
	}
	if config.OciRef != "registry.example.com/models/llama-3-70b:v1" {
		t.Errorf("OciRef = %q, want %q", config.OciRef, "registry.example.com/models/llama-3-70b:v1")
	}

	// Registering with an empty ref should fail.
	_, err = catalog.Register(ctx, "")
	if err == nil {
		t.Error("expected error for empty ociRef")
	}
}

func TestGet(t *testing.T) {
	catalog := NewInMemoryModelCatalog(nil)
	ctx := context.Background()

	ref := "registry.example.com/models/phi-3:v1"
	registered, err := catalog.Register(ctx, ref)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	got, err := catalog.Get(ctx, registered.Descriptor.Name)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.OciRef != ref {
		t.Errorf("Get() OciRef = %q, want %q", got.OciRef, ref)
	}

	// Getting a non-existent model should fail.
	_, err = catalog.Get(ctx, "nonexistent-model")
	if err == nil {
		t.Error("expected error for non-existent model")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestList(t *testing.T) {
	catalog := NewInMemoryModelCatalog(nil)
	ctx := context.Background()

	// Empty catalog.
	models, err := catalog.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("List() returned %d models, want 0", len(models))
	}

	// Register two models.
	_, err = catalog.Register(ctx, "registry.example.com/models/model-a:v1")
	if err != nil {
		t.Fatalf("Register(model-a) error: %v", err)
	}
	_, err = catalog.Register(ctx, "registry.example.com/models/model-b:v1")
	if err != nil {
		t.Fatalf("Register(model-b) error: %v", err)
	}

	models, err = catalog.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(models) != 2 {
		t.Errorf("List() returned %d models, want 2", len(models))
	}
}

func TestDeregister(t *testing.T) {
	catalog := NewInMemoryModelCatalog(nil)
	ctx := context.Background()

	ref := "registry.example.com/models/to-remove:v1"
	registered, err := catalog.Register(ctx, ref)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	err = catalog.Deregister(ctx, registered.Descriptor.Name)
	if err != nil {
		t.Fatalf("Deregister() error: %v", err)
	}

	// Should no longer be found.
	_, err = catalog.Get(ctx, registered.Descriptor.Name)
	if err == nil {
		t.Error("expected error after deregistration")
	}

	// Deregistering again should fail.
	err = catalog.Deregister(ctx, registered.Descriptor.Name)
	if err == nil {
		t.Error("expected error for double deregistration")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}
