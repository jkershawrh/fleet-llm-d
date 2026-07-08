package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSOptions configures TLS behavior for HTTP clients.
type TLSOptions struct {
	CAPath             string
	InsecureSkipVerify bool
}

// NewTLSConfig creates a *tls.Config from the given options.
// By default (empty options), it uses system CA certificates and verifies.
func NewTLSConfig(opts TLSOptions) (*tls.Config, error) {
	if opts.InsecureSkipVerify {
		return &tls.Config{InsecureSkipVerify: true}, nil //nolint:gosec // caller explicitly opted in
	}

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

	return &tls.Config{RootCAs: pool}, nil
}
