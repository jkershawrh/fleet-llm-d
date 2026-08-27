package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	gridsignals "github.com/llm-d/fleet-llm-d/pkg/routing/signals"
)

type config struct {
	site, provider, sourceURL       string
	healthURL                       string
	listenAddress, healthAddress    string
	certPath, keyPath, clientCAPath string
	peerFingerprints                string
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.site, "site", env("GRID_SIGNALS_SITE", ""), "fleet site identity")
	flag.StringVar(&cfg.provider, "provider", env("GRID_SIGNALS_PROVIDER", ""), "pool/provider identity")
	flag.StringVar(&cfg.sourceURL, "source-url", env("GRID_SIGNALS_SOURCE_URL", "http://127.0.0.1:9090/metrics"), "local EPP Prometheus URL")
	flag.StringVar(&cfg.healthURL, "health-url", env("GRID_SIGNALS_HEALTH_URL", ""), "optional provider health URL")
	flag.StringVar(&cfg.listenAddress, "listen-address", env("GRID_SIGNALS_LISTEN_ADDRESS", ":9443"), "mTLS listen address")
	flag.StringVar(&cfg.healthAddress, "health-address", env("GRID_SIGNALS_HEALTH_ADDRESS", ":8081"), "Kubernetes probe health address")
	flag.StringVar(&cfg.certPath, "tls-cert", env("GRID_SIGNALS_TLS_CERT", ""), "server certificate path")
	flag.StringVar(&cfg.keyPath, "tls-key", env("GRID_SIGNALS_TLS_KEY", ""), "server private key path")
	flag.StringVar(&cfg.clientCAPath, "client-ca", env("GRID_SIGNALS_CLIENT_CA", ""), "trusted client CA path")
	flag.StringVar(&cfg.peerFingerprints, "peer-fingerprints", env("GRID_SIGNALS_PEER_FINGERPRINTS", ""), "comma-separated SHA-256 client certificate fingerprints")
	flag.Parse()
	if err := run(cfg); err != nil {
		slog.Error("grid signals publisher stopped", "error", err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	if strings.TrimSpace(cfg.site) == "" || strings.TrimSpace(cfg.provider) == "" {
		return errors.New("site and provider are required")
	}
	tlsConfig, err := serverTLSConfig(cfg.certPath, cfg.keyPath, cfg.clientCAPath)
	if err != nil {
		return err
	}
	peers, err := parseFingerprints(cfg.peerFingerprints)
	if err != nil {
		return err
	}
	allowed := map[string]struct{}{
		"llm_d_epp_average_queue_size":           {},
		"llm_d_epp_average_kv_cache_utilization": {},
		"llm_d_epp_ready_endpoints":              {},
		"llm_d_epp_flow_control_pool_saturation": {},
	}
	store := &gridsignals.ProviderStore{
		Metrics:   &gridsignals.PrometheusStore{Endpoint: cfg.sourceURL},
		HealthURL: cfg.healthURL,
	}
	publisher := &gridsignals.Publisher{
		Site: cfg.site, Provider: cfg.provider,
		Store:          store,
		AllowedMetrics: allowed, AllowedPeerFingerprints: peers,
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", publisher)
	server := &http.Server{Addr: cfg.listenAddress, Handler: mux, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second}
	health := &http.Server{Addr: cfg.healthAddress, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), ReadHeaderTimeout: 2 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 2)
	go func() { errs <- health.ListenAndServe() }()
	go func() { errs <- server.ListenAndServeTLS("", "") }()
	slog.Info("grid signals publisher started", "site", cfg.site, "provider", cfg.provider, "address", cfg.listenAddress)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdownCtx)
		return server.Shutdown(shutdownCtx)
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func serverTLSConfig(certPath, keyPath, caPath string) (*tls.Config, error) {
	if certPath == "" || keyPath == "" || caPath == "" {
		return nil, errors.New("TLS certificate, key, and client CA are required")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load server identity: %w", err)
	}
	data, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("client CA contains no certificates")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, nil
}

func parseFingerprints(raw string) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		decoded, _ := pem.Decode([]byte(value))
		if decoded != nil {
			return nil, errors.New("peer fingerprints must be SHA-256 hex, not PEM")
		}
		if len(value) != 64 || strings.Trim(value, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("invalid peer certificate fingerprint %q", value)
		}
		result[value] = struct{}{}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one peer certificate fingerprint is required")
	}
	return result, nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
