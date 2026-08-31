package modelpack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolve_ValidRef(t *testing.T) {
	config := ModelPackConfig{
		Descriptor: ModelDescriptor{
			Name:    "llama-3-70b",
			Family:  "llama",
			Version: "v1",
			Vendor:  "meta",
			License: "llama3",
		},
		Config: ModelTechnicalConfig{
			Architecture: "transformer",
			Format:       "safetensors",
			ParamSize:    "70b",
			Precision:    "float16",
			InputTypes:   []string{"text"},
			OutputTypes:  []string{"text"},
		},
	}
	configBytes, _ := json.Marshal(config)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/models/llama-3-70b/manifests/v1":
			_ = json.NewEncoder(w).Encode(ociManifest{
				Config: ociDescriptor{
					MediaType: modelConfigMediaType,
					Digest:    "sha256:testconfig",
					Size:      int64(len(configBytes)),
				},
			})
		case "/v2/models/llama-3-70b/blobs/sha256:testconfig":
			w.Header().Set("Content-Type", modelConfigMediaType)
			_, _ = w.Write(configBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver := NewRegistryModelResolver(WithRegistryScheme("http"))
	ref := strings.TrimPrefix(server.URL, "http://") + "/models/llama-3-70b:v1"

	got, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if got.Descriptor.Name != "llama-3-70b" {
		t.Fatalf("expected resolved model name, got %q", got.Descriptor.Name)
	}
	if got.OciRef != ref {
		t.Fatalf("expected OciRef %q, got %q", ref, got.OciRef)
	}
}

func TestResolve_NotFound(t *testing.T) {
	resolver := NewRegistryModelResolver()
	ref := "registry.example.com/models/nonexistent-model:latest"

	_, err := resolver.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error for non-existent model, got nil")
	}

	if !strings.Contains(err.Error(), "registry") && !strings.Contains(err.Error(), "Bad Gateway") {
		t.Errorf("expected registry resolution error, got: %v", err)
	}
}

func TestResolver_RejectsUnsignedModel(t *testing.T) {
	// Serve a valid model manifest and config, but no signature tag.
	config := ModelPackConfig{
		Descriptor: ModelDescriptor{Name: "unsigned-model", Family: "test", Version: "v1"},
		Config:     ModelTechnicalConfig{Architecture: "transformer", Format: "safetensors"},
	}
	configBytes, _ := json.Marshal(config)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/models/unsigned-model/manifests/v1":
			_ = json.NewEncoder(w).Encode(ociManifest{
				Config: ociDescriptor{
					MediaType: modelConfigMediaType,
					Digest:    "sha256:testconfig",
					Size:      int64(len(configBytes)),
				},
			})
		case "/v2/models/unsigned-model/blobs/sha256:testconfig":
			w.Header().Set("Content-Type", modelConfigMediaType)
			_, _ = w.Write(configBytes)
		default:
			// No signature tag served — return 404
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver := NewRegistryModelResolver(
		WithRegistryScheme("http"),
		WithRequireSignature(true),
	)
	ref := strings.TrimPrefix(server.URL, "http://") + "/models/unsigned-model:v1"

	_, err := resolver.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("should reject unsigned model when RequireSignature is true")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("error should mention signature, got: %v", err)
	}
}

func TestResolver_AcceptsWhenSignatureNotRequired(t *testing.T) {
	// Same server as above — valid model, no signature. With RequireSignature
	// disabled the resolve should succeed without checking signatures.
	config := ModelPackConfig{
		Descriptor: ModelDescriptor{Name: "any-model", Family: "test", Version: "v1"},
		Config:     ModelTechnicalConfig{Architecture: "transformer", Format: "safetensors"},
	}
	configBytes, _ := json.Marshal(config)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/models/any-model/manifests/v1":
			_ = json.NewEncoder(w).Encode(ociManifest{
				Config: ociDescriptor{
					MediaType: modelConfigMediaType,
					Digest:    "sha256:testconfig",
					Size:      int64(len(configBytes)),
				},
			})
		case "/v2/models/any-model/blobs/sha256:testconfig":
			w.Header().Set("Content-Type", modelConfigMediaType)
			_, _ = w.Write(configBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver := NewRegistryModelResolver(WithRegistryScheme("http"))
	ref := strings.TrimPrefix(server.URL, "http://") + "/models/any-model:v1"

	_, err := resolver.Resolve(context.Background(), ref)
	if err != nil && strings.Contains(err.Error(), "signature") {
		t.Error("should not check signatures when RequireSignature is false")
	}
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
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
