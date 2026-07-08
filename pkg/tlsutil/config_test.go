package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func TestNewTLSConfig_DefaultVerifiesSystemCAs(t *testing.T) {
	cfg, err := NewTLSConfig(TLSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InsecureSkipVerify {
		t.Error("default TLS config should verify certificates")
	}
}

func TestNewTLSConfig_InsecureSkipVerify(t *testing.T) {
	cfg, err := NewTLSConfig(TLSOptions{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when requested")
	}
}

// generateTestCA creates a self-signed CA certificate PEM for testing.
func generateTestCA(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

func TestNewTLSConfig_CustomCA(t *testing.T) {
	caPEM := generateTestCA(t)

	f, err := os.CreateTemp("", "test-ca-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Write(caPEM)
	f.Close()

	cfg, err := NewTLSConfig(TLSOptions{CAPath: f.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil {
		t.Error("custom CA should populate RootCAs")
	}
	if cfg.InsecureSkipVerify {
		t.Error("should verify certificates when custom CA is provided")
	}
}

func TestNewTLSConfig_InvalidCAPath(t *testing.T) {
	_, err := NewTLSConfig(TLSOptions{CAPath: "/nonexistent/ca.pem"})
	if err == nil {
		t.Error("expected error for nonexistent CA path")
	}
}

func TestNewTLSConfig_InvalidPEMContent(t *testing.T) {
	f, err := os.CreateTemp("", "test-bad-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Write([]byte("this is not a valid PEM"))
	f.Close()

	_, err = NewTLSConfig(TLSOptions{CAPath: f.Name()})
	if err == nil {
		t.Error("expected error for invalid PEM content")
	}
}
