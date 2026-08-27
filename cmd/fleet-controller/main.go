package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
	fleetcontroller "github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/intents"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/routing"
	"github.com/llm-d/fleet-llm-d/pkg/server"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
)

func main() {
	port := flag.Int("port", 8080, "API server port")
	metricsPort := flag.Int("metrics-port", 9091, "Metrics server port")
	grpcPort := flag.Int("grpc-port", 0, "gRPC (JSON-RPC) server port; 0 disables")
	mode := flag.String("mode", "all", "Server mode: all (default), control (fleet API only), inference (OpenAI gateway only)")
	production := flag.Bool("production", false, "Require production-safe authentication, persistence, ledger, TLS, and signing configuration")
	ledgerMode := flag.String("ledger-mode", string(ledger.ModeDisabled), "Ledger backend mode: disabled (default), memory (development only -- evidence is fabricated and lost on restart), http (standalone are-immutable-ledger REST compatibility gateway). gRPC is canonical upstream but not yet generated in this binary")
	ledgerEndpoint := flag.String("ledger-endpoint", "http://localhost:18099", "standalone immutable-ledger REST gateway endpoint (HTTP compatibility mode only)")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file")
	_ = flag.String("backend-vllm", "", "Deprecated: inference routing moved to Praxis")
	_ = flag.String("backend-ovms", "", "Deprecated: inference routing moved to Praxis")
	kubeAPI := flag.String("kube-api", "", "Kubernetes API server URL (enables CRD watching and authoritative intent persistence when set)")
	namespace := flag.String("namespace", "default", "Kubernetes namespace to watch for FleetInferencePool CRDs")
	pgURL := flag.String("pg-url", "", "PostgreSQL connection string (e.g. postgres://user:pass@host:5432/fleet?sslmode=disable). When set, uses PostgreSQL instead of in-memory stores")
	eventEndpoint := flag.String("event-endpoint", "", "HTTP endpoint for publishing fleet events (e.g. http://kafka-bridge:8080/topics/fleet-events). When set, events are also POSTed to this URL")
	modelplaneAPI := flag.String("modelplane-api", "", "ModelPlane API server URL (enables ModelPlane integration when set)")
	modelplaneNamespace := flag.String("modelplane-namespace", "default", "ModelPlane namespace to watch for resources")
	rateLimit := flag.Float64("rate-limit", 100, "Rate limit in requests per second per IP (0 to disable)")
	rateBurst := flag.Int("rate-burst", 200, "Rate limit burst size (max requests before throttling)")
	rateLimitExempt := flag.String("rate-limit-exempt", "/healthz,/readyz,/metrics", "Comma-separated exact paths exempt from rate limiting and auth")
	trustProxyHeaders := flag.Bool("trust-proxy-headers", false, "Honour X-Forwarded-For when identifying clients for rate limiting. Enable ONLY when every request arrives through a proxy that overwrites the header; otherwise clients can forge their own rate-limit identity")
	praxisURL := flag.String("praxis-url", "", "Internal Praxis inference endpoint")
	inferenceProvider := flag.String("inference-provider", "", "Inference data plane: praxis (default) or llm-d-router")
	llmdCPUURL := flag.String("llm-d-router-cpu-url", "", "Internal llm-d Router CPU proxy endpoint")
	llmdGPUURL := flag.String("llm-d-router-gpu-url", "", "Internal llm-d Router GPU proxy endpoint")
	routingProvider := flag.String("routing-provider", "", "Authoritative routing adapter: praxis (default), llm-d-router, or disabled")
	inferenceMaxInflight := flag.Int("inference-max-inflight", 100, "Maximum concurrent inference requests per gateway replica")
	_ = flag.String("backends", "", "Deprecated: inference routing moved to Praxis")
	_ = flag.Int("max-inflight", 0, "Deprecated: load shedding moved to Praxis")
	allowOperatorJSONIntents := flag.Bool("allow-operator-json-intents", false, "Enable unsigned application/json v2 intent input for development/operator compatibility only")
	flag.Parse()
	if *praxisURL == "" {
		*praxisURL = os.Getenv("PRAXIS_URL")
	}
	if *inferenceProvider == "" {
		*inferenceProvider = os.Getenv("FLEET_INFERENCE_PROVIDER")
	}
	parsedInferenceProvider, err := server.ParseInferenceProviderName(*inferenceProvider)
	if err != nil {
		slog.Error("invalid inference provider", "error", err)
		os.Exit(1)
	}
	if *llmdCPUURL == "" {
		*llmdCPUURL = os.Getenv("FLEET_LLMD_ROUTER_CPU_URL")
	}
	if *llmdGPUURL == "" {
		*llmdGPUURL = os.Getenv("FLEET_LLMD_ROUTER_GPU_URL")
	}
	if *routingProvider == "" {
		*routingProvider = os.Getenv("FLEET_ROUTING_PROVIDER")
	}
	parsedRoutingProvider, err := routing.ParseProviderName(*routingProvider)
	if err != nil {
		slog.Error("invalid routing provider", "error", err)
		os.Exit(1)
	}
	if err := os.Setenv("FLEET_ROUTING_PROVIDER", string(parsedRoutingProvider)); err != nil {
		slog.Error("configure routing provider", "error", err)
		os.Exit(1)
	}

	if *pgURL == "" {
		if env := os.Getenv("PG_URL"); env != "" {
			*pgURL = env
		}
	}

	// Configure structured JSON logging.
	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	slog.Info("fleet-controller starting", "mode", *mode, "log_level", *logLevel, "ledger_mode", *ledgerMode, "ledger", *ledgerEndpoint, "grpc_port", *grpcPort)

	if ledger.Mode(*ledgerMode) == ledger.ModeMemory {
		slog.Warn("ledger mode is 'memory': receipts are fabricated in-process and lost on restart. " +
			"This is NOT tamper-evident evidence. Use --ledger-mode=http against the standalone " +
			"are-immutable-ledger for anything that must be auditable.")
	}

	authCfg, err := auth.ConfigFromEnv()
	if err != nil {
		slog.Error("invalid authentication configuration", "error", err)
		os.Exit(1)
	}
	if !authCfg.Enabled {
		slog.Warn("authentication is DISABLED: every API request will be served unauthenticated. " +
			"Set FLEET_AUTH_SECRET or FLEET_AUTH_SECRET_FILE before exposing this controller.")
	}
	if err := validateProductionConfig(*production, *mode, *pgURL, ledger.Mode(*ledgerMode), *ledgerEndpoint, authCfg.Enabled, *tlsCert, *tlsKey, parsedInferenceProvider, *praxisURL, *llmdCPUURL, *llmdGPUURL, os.Getenv("GCL_DECISION_SIGNING_KEYS_JSON")); err != nil {
		slog.Error("production configuration rejected", "error", err)
		os.Exit(1)
	}
	slog.Info("configuration loaded", "auth_enabled", authCfg.Enabled, "tls_enabled", *tlsCert != "" && *tlsKey != "", "kube_api", *kubeAPI, "namespace", *namespace, "postgres", *pgURL != "", "event_endpoint", *eventEndpoint)

	fc, err := server.NewFleetControllerWithLedgerConfig(ledger.Config{
		Mode:     ledger.Mode(*ledgerMode),
		Endpoint: *ledgerEndpoint,
		APIToken: os.Getenv("LEDGER_GATEWAY_API_TOKEN"),
	}, "", "", *kubeAPI, *namespace)
	if err != nil {
		slog.Error("invalid immutable-ledger configuration", "error", err)
		os.Exit(1)
	}
	if *kubeAPI != "" && *mode != "inference" {
		identity := os.Getenv("POD_NAME")
		if identity == "" {
			identity, err = os.Hostname()
			if err != nil || identity == "" {
				slog.Error("leader election requires POD_NAME or a resolvable hostname")
				os.Exit(1)
			}
		}
		fc.ConfigureLeaderElection(fleetcontroller.NewLeaderElector(*kubeAPI, *namespace, identity))
		slog.Info("leader election enabled", "identity", identity, "namespace", *namespace)
	}

	decisionKeys, err := server.DecisionPackageKeyringFromEnv()
	if err != nil {
		slog.Error("invalid GCL DecisionPackage verification configuration", "error", err)
		os.Exit(1)
	}
	if len(decisionKeys) > 0 {
		allowLegacyHMAC := os.Getenv("FLEET_ALLOW_HMAC_DECISION_PACKAGES") == "true"
		fc.DecisionPackageDecoder = intents.NewGCLDecisionPackageDecoderWithPolicy(decisionKeys, allowLegacyHMAC)
		slog.Info("GCL DecisionPackage verification enabled",
			"trusted_keys", len(decisionKeys), "allow_legacy_hmac", allowLegacyHMAC)
		if allowLegacyHMAC {
			slog.Warn("legacy HMAC-SHA256 DecisionPackages are accepted: the signing key is shared with GCL, " +
				"so a signature no longer proves GCL authorship. Unset " +
				"FLEET_ALLOW_HMAC_DECISION_PACKAGES once GCL signs with Ed25519.")
		}
	}
	fc.AllowOperatorJSONIntents = server.OperatorJSONIntentsEnabled(*allowOperatorJSONIntents)
	if fc.AllowOperatorJSONIntents {
		slog.Warn("unsigned application/json v2 intent compatibility is enabled; do not use this ingress as GCL provenance")
	}

	fc.AuthSecret = authCfg.Secret
	fc.InferenceProviderName = parsedInferenceProvider
	fc.PraxisURL = *praxisURL
	fc.PraxisToken = os.Getenv("PRAXIS_API_TOKEN")
	fc.LLMDCPUURL = *llmdCPUURL
	fc.LLMDGPUURL = *llmdGPUURL
	fc.LLMDToken = os.Getenv("FLEET_LLMD_ROUTER_API_TOKEN")
	fc.CPUPhysicalModel = os.Getenv("FLEET_CPU_PHYSICAL_MODEL")
	fc.GPUPhysicalModel = os.Getenv("FLEET_GPU_PHYSICAL_MODEL")
	if raw := os.Getenv("FLEET_PROVIDER_HEALTH_URLS_JSON"); raw != "" {
		var providerURLs map[string]string
		if err := json.Unmarshal([]byte(raw), &providerURLs); err != nil {
			slog.Error("invalid provider health URL configuration", "error", err)
			os.Exit(1)
		}
		providerHealth, err := server.NewProviderHealthCache(providerURLs, os.Getenv("FLEET_PROVIDER_CA_FILE"))
		if err != nil {
			slog.Error("invalid provider health probe configuration", "error", err)
			os.Exit(1)
		}
		fc.ProviderHealth = providerHealth
		providerHealth.Start(context.Background())
		slog.Info("active provider health probes enabled", "providers", len(providerURLs))
	}
	if *inferenceMaxInflight > 0 {
		fc.InferenceSlots = make(chan struct{}, *inferenceMaxInflight)
	}
	fc.InferenceClient = &http.Client{Timeout: 180 * time.Second}

	if *pgURL != "" {
		db, err := sql.Open("postgres", *pgURL)
		if err != nil {
			slog.Error("failed to open postgres", "error", err)
			os.Exit(1)
		}
		defer db.Close()
		if err := fc.OverrideWithPostgres(db); err != nil {
			slog.Error("postgres override failed", "error", err)
			os.Exit(1)
		}
	}

	fc.InitGauges(context.Background())

	if *eventEndpoint != "" {
		fc.EventPublisher = events.NewLedgerAwarePublisher(events.NewHTTPEventPublisher(*eventEndpoint), fc.FleetRecorder)
		slog.Info("event publishing enabled", "endpoint", *eventEndpoint)
	}

	if *modelplaneAPI != "" {
		fc.WireModelPlane(*modelplaneAPI, *modelplaneNamespace)
	}

	var rl *auth.RateLimiter
	if *rateLimit > 0 {
		rl = auth.NewRateLimiter(*rateLimit, *rateBurst)
		rl.TrustProxyHeaders(*trustProxyHeaders)
		defer rl.Stop()
		slog.Info("rate limiting enabled",
			"rate", *rateLimit, "burst", *rateBurst,
			"trust_proxy_headers", *trustProxyHeaders)
	}

	if err := fc.Run(context.Background(), *port, *metricsPort, *grpcPort, authCfg, *tlsCert, *tlsKey, *mode, rl, server.SplitCSV(*rateLimitExempt)); err != nil {
		slog.Error("fleet-controller exited", "error", err)
		os.Exit(1)
	}
}

func validateProductionConfig(production bool, mode, pgURL string, ledgerMode ledger.Mode, ledgerEndpoint string, authEnabled bool, tlsCert, tlsKey string, inferenceProvider server.InferenceProviderName, praxisURL, llmdCPUURL, llmdGPUURL, decisionKeys string) error {
	if !production {
		return nil
	}
	var missing []string
	if !authEnabled {
		missing = append(missing, "authentication")
	}
	if !productionPostgresURLValid(pgURL) {
		missing = append(missing, "TLS PostgreSQL")
	}
	if ledgerMode != ledger.ModeHTTP || !productionHTTPSURLValid(ledgerEndpoint) || os.Getenv("LEDGER_GATEWAY_API_TOKEN") == "" {
		missing = append(missing, "authenticated HTTPS immutable ledger")
	}
	if tlsCert == "" || tlsKey == "" {
		missing = append(missing, "API TLS certificate and key")
	}
	if decisionKeys == "" {
		missing = append(missing, "GCL Ed25519 verification keys")
	}
	if mode == "all" || mode == "inference" {
		switch inferenceProvider {
		case server.InferenceProviderPraxis:
			if praxisURL == "" {
				missing = append(missing, "Praxis URL")
			}
		case server.InferenceProviderLLMD:
			if llmdCPUURL == "" || llmdGPUURL == "" {
				missing = append(missing, "llm-d Router CPU and GPU URLs")
			}
		default:
			missing = append(missing, "supported inference provider")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing or unsafe production requirements: %s", strings.Join(missing, ", "))
	}
	if mode != "all" && mode != "control" && mode != "inference" {
		return fmt.Errorf("invalid server mode %q", mode)
	}
	return nil
}

func productionPostgresURLValid(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return false
	}
	return parsed.Query().Get("sslmode") == "verify-full"
}

func productionHTTPSURLValid(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != ""
}
