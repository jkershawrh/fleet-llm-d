package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNewFleetClient_WithoutCA_UsesDefaults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	client := NewFleetClient(ts.URL, "")
	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("request should succeed with plain HTTP: %v", err)
	}
	if clusters == nil {
		t.Error("expected non-nil clusters")
	}
}

func TestNewFleetClient_WithCA_ConfiguresTLS(t *testing.T) {
	// Create a TLS server
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	// Write the test server's CA certificate to a temp file
	certPool := x509.NewCertPool()
	for _, cert := range ts.TLS.Certificates {
		for _, c := range cert.Certificate {
			parsed, err := x509.ParseCertificate(c)
			if err != nil {
				t.Fatal(err)
			}
			certPool.AddCert(parsed)
		}
	}

	// Write PEM-encoded cert to temp file
	caFile, err := os.CreateTemp("", "test-ca-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(caFile.Name())

	// Get the PEM from the test server's certificate
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: ts.TLS.Certificates[0].Certificate[0],
	})
	if _, err := caFile.Write(pemData); err != nil {
		t.Fatal(err)
	}
	caFile.Close()

	client := NewFleetClient(ts.URL, caFile.Name())
	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("request with valid CA should succeed: %v", err)
	}
	_ = clusters
}

func TestNewFleetClient_InvalidCAPath_LogsWarning(t *testing.T) {
	// Should not panic, should fall back to default client
	client := NewFleetClient("http://localhost:9999", "/nonexistent/ca.pem")
	if client == nil {
		t.Fatal("client should not be nil even with invalid CA path")
	}
}
