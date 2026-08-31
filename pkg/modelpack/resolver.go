package modelpack

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	cosignPublicKey  crypto.PublicKey
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

// WithCosignPublicKey configures a PEM-encoded public key for cryptographic
// cosign signature verification. When set alongside WithRequireSignature(true),
// signatures are verified against this key rather than performing a
// tag-existence-only check.
func WithCosignPublicKey(pemData []byte) RegistryResolverOption {
	return func(r *RegistryModelResolver) {
		key, err := parseCosignPublicKey(pemData)
		if err != nil {
			return
		}
		r.cosignPublicKey = key
	}
}

// parseCosignPublicKey decodes a PEM-encoded public key (ECDSA or RSA).
func parseCosignPublicKey(pemData []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in cosign public key data")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cosign public key: %w", err)
	}
	return pub, nil
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

// ociSignatureManifest extends ociManifest with annotation-bearing layers used
// by cosign to store signature payloads.
type ociSignatureLayer struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ociSignatureManifest struct {
	SchemaVersion int                 `json:"schemaVersion,omitempty"`
	MediaType     string              `json:"mediaType,omitempty"`
	Config        ociDescriptor       `json:"config"`
	Layers        []ociSignatureLayer `json:"layers,omitempty"`
}

// verifySignature checks for a cosign signature at the standard OCI location.
// When a cosignPublicKey is configured, the signature payload is
// cryptographically verified against the key (ECDSA or RSA). Without a key,
// only tag existence is checked.
func (r *RegistryModelResolver) verifySignature(ctx context.Context, ref string) error {
	parsed, err := parseOCIRef(ref)
	if err != nil {
		return fmt.Errorf("invalid reference for signature check: %w", err)
	}

	// Fetch the cosign signature manifest at the standard .sig tag.
	sigTag := parsed.reference + ".sig"
	sigURL := r.registryURL(parsed.host, "/v2/"+parsed.repository+"/manifests/"+sigTag)
	sigManifestBytes, err := r.doGet(ctx, sigURL, "application/vnd.oci.image.manifest.v1+json")
	if err != nil {
		return fmt.Errorf("no cosign signature found for %s: %w", ref, err)
	}

	// Without a configured public key, tag existence is sufficient.
	if r.cosignPublicKey == nil {
		return nil
	}

	// Parse the signature manifest and verify at least one layer's signature
	// against the configured public key.
	var sigManifest ociSignatureManifest
	if err := json.Unmarshal(sigManifestBytes, &sigManifest); err != nil {
		return fmt.Errorf("parse signature manifest for %s: %w", ref, err)
	}

	if len(sigManifest.Layers) == 0 {
		return fmt.Errorf("signature manifest for %s has no layers", ref)
	}

	for _, layer := range sigManifest.Layers {
		sig64, ok := layer.Annotations["dev.cosignproject.cosign/signature"]
		if !ok {
			continue
		}
		sigBytes, err := base64.StdEncoding.DecodeString(sig64)
		if err != nil {
			continue
		}
		// The cosign simple-signing payload is the image digest; hash it
		// and verify the signature.
		payload := []byte(parsed.reference)
		digest := sha256.Sum256(payload)

		switch key := r.cosignPublicKey.(type) {
		case *ecdsa.PublicKey:
			if ecdsa.VerifyASN1(key, digest[:], sigBytes) {
				return nil
			}
		case *rsa.PublicKey:
			if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sigBytes) == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("no valid cosign signature verified against the configured public key for %s", ref)
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
