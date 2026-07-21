package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"os"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
	fleetcontroller "github.com/llm-d/fleet-llm-d/pkg/controller"
	"github.com/llm-d/fleet-llm-d/pkg/intents"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
	"github.com/llm-d/fleet-llm-d/pkg/server"
	"github.com/llm-d/fleet-llm-d/pkg/store/events"
)

func main() {
	port := flag.Int("port", 8080, "API server port")
	metricsPort := flag.Int("metrics-port", 9091, "Metrics server port")
	grpcPort := flag.Int("grpc-port", 0, "gRPC (JSON-RPC) server port; 0 disables")
	mode := flag.String("mode", "all", "Server mode: all (default), control (fleet API only), inference (inference proxy only)")
	ledgerMode := flag.String("ledger-mode", string(ledger.ModeMemory), "Ledger backend mode: disabled, memory, http (gRPC is canonical upstream but not yet generated in this binary)")
	ledgerEndpoint := flag.String("ledger-endpoint", "http://localhost:18099", "standalone immutable-ledger REST gateway endpoint (HTTP compatibility mode only)")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file")
	backendVLLM := flag.String("backend-vllm", "http://vllm-cpu.fleet-llm-d.svc:8000", "Base URL for the vLLM inference backend")
	backendOVMS := flag.String("backend-ovms", "http://ovms-granite-external.fleet-llm-d.svc:8080", "Base URL for the OVMS inference backend")
	kubeAPI := flag.String("kube-api", "", "Kubernetes API server URL (enables CRD watching and authoritative intent persistence when set)")
	namespace := flag.String("namespace", "default", "Kubernetes namespace to watch for FleetInferencePool CRDs")
	pgURL := flag.String("pg-url", "", "PostgreSQL connection string (e.g. postgres://user:pass@host:5432/fleet?sslmode=disable). When set, uses PostgreSQL instead of in-memory stores")
	eventEndpoint := flag.String("event-endpoint", "", "HTTP endpoint for publishing fleet events (e.g. http://kafka-bridge:8080/topics/fleet-events). When set, events are also POSTed to this URL")
	modelplaneAPI := flag.String("modelplane-api", "", "ModelPlane API server URL (enables ModelPlane integration when set)")
	modelplaneNamespace := flag.String("modelplane-namespace", "default", "ModelPlane namespace to watch for resources")
	rateLimit := flag.Float64("rate-limit", 100, "Rate limit in requests per second per IP (0 to disable)")
	rateBurst := flag.Int("rate-burst", 200, "Rate limit burst size (max requests before throttling)")
	rateLimitExempt := flag.String("rate-limit-exempt", "/healthz,/readyz,/metrics", "Comma-separated exact paths exempt from rate limiting and auth")
	backends := flag.String("backends", "", `JSON array of inference backends: [{"model":"name","url":"http://...","runtime":"openvino|vllm","path_prefix":"/v3"}]`)
	maxInflight := flag.Int("max-inflight", 0, "Max concurrent inference requests per model (0 = disabled)")
	allowOperatorJSONIntents := flag.Bool("allow-operator-json-intents", false, "Enable unsigned application/json v2 intent input for development/operator compatibility only")
	flag.Parse()

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

	authCfg := auth.ConfigFromEnv()
	slog.Info("configuration loaded", "auth_enabled", authCfg.Enabled, "tls_enabled", *tlsCert != "" && *tlsKey != "", "kube_api", *kubeAPI, "namespace", *namespace, "postgres", *pgURL != "", "event_endpoint", *eventEndpoint)

	fc, err := server.NewFleetControllerWithLedgerConfig(ledger.Config{
		Mode:     ledger.Mode(*ledgerMode),
		Endpoint: *ledgerEndpoint,
		APIToken: os.Getenv("LEDGER_GATEWAY_API_TOKEN"),
	}, *backendVLLM, *backendOVMS, *kubeAPI, *namespace)
	if err != nil {
		slog.Error("invalid immutable-ledger configuration", "error", err)
		os.Exit(1)
	}
	if *kubeAPI != "" {
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
		fc.DecisionPackageDecoder = intents.NewGCLDecisionPackageDecoder(decisionKeys)
		slog.Info("GCL DecisionPackage verification enabled", "trusted_keys", len(decisionKeys))
	}
	fc.AllowOperatorJSONIntents = server.OperatorJSONIntentsEnabled(*allowOperatorJSONIntents)
	if fc.AllowOperatorJSONIntents {
		slog.Warn("unsigned application/json v2 intent compatibility is enabled; do not use this ingress as GCL provenance")
	}

	fc.AuthSecret = authCfg.Secret
	fc.InferenceProxy.SetMaxInflight(*maxInflight)

	if *backends != "" {
		if err := fc.RegisterBackendsFromJSON(*backends); err != nil {
			slog.Error("failed to register backends", "error", err)
			os.Exit(1)
		}
	}

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
		slog.Info("rate limiting enabled", "rate", *rateLimit, "burst", *rateBurst)
	}

	if err := fc.Run(context.Background(), *port, *metricsPort, *grpcPort, authCfg, *tlsCert, *tlsKey, *mode, rl, server.SplitCSV(*rateLimitExempt)); err != nil {
		slog.Error("fleet-controller exited", "error", err)
		os.Exit(1)
	}
}
