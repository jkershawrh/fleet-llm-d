package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

const (
	ServiceAccountCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	ServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// TLSOptions configures TLS behavior for HTTP clients.
type TLSOptions struct {
	CAPath string
}

// NewTLSConfig creates a *tls.Config from the given options.
// By default (empty options), it uses system CA certificates and verifies.
func NewTLSConfig(opts TLSOptions) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}

	if opts.CAPath != "" {
		pem, err := os.ReadFile(opts.CAPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA file %s: %w", opts.CAPath, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificates found in %s", opts.CAPath)
		}
	}

	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}, nil
}

// KubernetesTLSOptions trusts the mounted service-account CA when running in
// a pod and otherwise uses the host's system trust store.
func KubernetesTLSOptions() TLSOptions {
	opts := TLSOptions{}
	if _, err := os.Stat(ServiceAccountCAPath); err == nil {
		opts.CAPath = ServiceAccountCAPath
	}
	return opts
}
