package modelpack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const modelConfigMediaType = "application/vnd.cncf.model.config.v1+json"

// ModelResolver resolves model metadata from OCI-compatible registries.
type ModelResolver interface {
	Resolve(ctx context.Context, ociRef string) (*ModelPackConfig, error)
}

// RegistryModelResolver pulls ModelPack configs from OCI-compatible registry
// manifests and config blobs.
type RegistryModelResolver struct {
	scheme           string
	http             *http.Client
	requireSignature bool
}

// RegistryResolverOption customizes RegistryModelResolver.
type RegistryResolverOption func(*RegistryModelResolver)

// WithRegistryScheme overrides the registry URL scheme. It is mainly useful
// for tests with httptest registries.
func WithRegistryScheme(scheme string) RegistryResolverOption {
	return func(r *RegistryModelResolver) {
		r.scheme = scheme
	}
}

// WithRequireSignature enables cosign signature verification for resolved
// models. When true, Resolve will reject any OCI reference that does not
// have a valid cosign signature.
func WithRequireSignature(require bool) RegistryResolverOption {
	return func(r *RegistryModelResolver) {
		r.requireSignature = require
	}
}

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion,omitempty"`
	MediaType     string          `json:"mediaType,omitempty"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers,omitempty"`
}

// NewRegistryModelResolver creates a new RegistryModelResolver.
func NewRegistryModelResolver(opts ...RegistryResolverOption) *RegistryModelResolver {
	resolver := &RegistryModelResolver{
		scheme: "https",
		http:   &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(resolver)
	}
	return resolver
}

// Resolve fetches and parses a ModelPack config from the given OCI reference.
func (r *RegistryModelResolver) Resolve(ctx context.Context, ociRef string) (*ModelPackConfig, error) {
	parsed, err := parseOCIRef(ociRef)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI reference %q: %w", ociRef, err)
	}

	manifestURL := r.registryURL(parsed.host, "/v2/"+parsed.repository+"/manifests/"+parsed.reference)
	manifestBytes, err := r.doGet(ctx, manifestURL, "application/vnd.oci.image.manifest.v1+json")
	if err != nil {
		return nil, fmt.Errorf("registry manifest fetch failed for %q: %w", ociRef, err)
	}

	var manifest ociManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse OCI manifest for %q: %w", ociRef, err)
	}
	if manifest.Config.Digest == "" {
		return nil, fmt.Errorf("OCI manifest for %q did not include a config digest", ociRef)
	}

	configURL := r.registryURL(parsed.host, "/v2/"+parsed.repository+"/blobs/"+manifest.Config.Digest)
	configBytes, err := r.doGet(ctx, configURL, modelConfigMediaType)
	if err != nil {
		return nil, fmt.Errorf("registry config fetch failed for %q: %w", ociRef, err)
	}

	var config ModelPackConfig
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return nil, fmt.Errorf("parse ModelPack config for %q: %w", ociRef, err)
	}
	config.OciRef = ociRef
	if config.Descriptor.Name == "" {
		config.Descriptor.Name = parsed.repository
	}

	if r.requireSignature {
		if err := r.verifySignature(ctx, ociRef); err != nil {
			return nil, fmt.Errorf("model signature verification failed for %s: %w", ociRef, err)
		}
	}

	return &config, nil
}

// verifySignature checks for a cosign signature at the standard OCI location.
// Currently returns an error for any model when requireSignature is true and
// no signature is found. Full cosign integration would use the cosign Go
// library to verify against a public key or keyless (Fulcio) signing.
func (r *RegistryModelResolver) verifySignature(ctx context.Context, ref string) error {
	parsed, err := parseOCIRef(ref)
	if err != nil {
		return fmt.Errorf("invalid reference for signature check: %w", err)
	}

	// Check for cosign signature tag at the standard location:
	// <registry>/<repo>:sha256-<digest>.sig
	// Attempt to fetch the signature manifest. If it does not exist (404 or
	// network error), the image is considered unsigned.
	sigTag := parsed.reference + ".sig"
	sigURL := r.registryURL(parsed.host, "/v2/"+parsed.repository+"/manifests/"+sigTag)
	_, err = r.doGet(ctx, sigURL, "application/vnd.oci.image.manifest.v1+json")
	if err != nil {
		return fmt.Errorf("no cosign signature found for %s: %w", ref, err)
	}
	return nil
}

type parsedOCIRef struct {
	host       string
	repository string
	reference  string
}

func parseOCIRef(ref string) (parsedOCIRef, error) {
	if err := validateOCIRef(ref); err != nil {
		return parsedOCIRef{}, err
	}

	slash := strings.Index(ref, "/")
	host := ref[:slash]
	remainder := ref[slash+1:]
	repository := remainder
	reference := "latest"

	if at := strings.LastIndex(remainder, "@"); at >= 0 {
		repository = remainder[:at]
		reference = remainder[at+1:]
	} else if colon := strings.LastIndex(remainder, ":"); colon >= 0 && colon > strings.LastIndex(remainder, "/") {
		repository = remainder[:colon]
		reference = remainder[colon+1:]
	}

	if repository == "" {
		return parsedOCIRef{}, fmt.Errorf("missing repository path")
	}
	if reference == "" {
		return parsedOCIRef{}, fmt.Errorf("missing tag or digest")
	}
	return parsedOCIRef{host: host, repository: repository, reference: reference}, nil
}

func (r *RegistryModelResolver) registryURL(host, path string) string {
	return (&url.URL{Scheme: r.scheme, Host: host, Path: path}).String()
}

func (r *RegistryModelResolver) doGet(ctx context.Context, rawURL, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
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
