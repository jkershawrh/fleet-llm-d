package modelpack

import (
	"context"
	"fmt"
	"strings"
)

// ModelResolver resolves model metadata from OCI-compatible registries.
type ModelResolver interface {
	Resolve(ctx context.Context, ociRef string) (*ModelPackConfig, error)
}

// RegistryModelResolver is a stub implementation of ModelResolver that will
// eventually pull ModelPack configs from OCI registries. Currently returns
// "not implemented" (TDD red phase).
type RegistryModelResolver struct{}

// NewRegistryModelResolver creates a new RegistryModelResolver.
func NewRegistryModelResolver() *RegistryModelResolver {
	return &RegistryModelResolver{}
}

// Resolve fetches and parses a ModelPack config from the given OCI reference.
func (r *RegistryModelResolver) Resolve(ctx context.Context, ociRef string) (*ModelPackConfig, error) {
	if err := validateOCIRef(ociRef); err != nil {
		return nil, fmt.Errorf("invalid OCI reference %q: %w", ociRef, err)
	}
	return nil, fmt.Errorf("not implemented: registry resolution for %q", ociRef)
}

// validateOCIRef performs basic validation of an OCI image reference.
// A valid reference must have at least a host and a repository path.
func validateOCIRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("empty reference")
	}

	// Must contain at least one slash (host/path).
	if !strings.Contains(ref, "/") {
		return fmt.Errorf("missing repository path")
	}

	// Host part must contain a dot or be localhost.
	host := ref[:strings.Index(ref, "/")]
	hostWithoutPort := strings.Split(host, ":")[0]
	if !strings.Contains(hostWithoutPort, ".") && hostWithoutPort != "localhost" {
		return fmt.Errorf("invalid registry host %q", host)
	}

	return nil
}
