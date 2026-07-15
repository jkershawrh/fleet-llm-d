package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
	"github.com/llm-d/fleet-llm-d/pkg/cluster/client"
	fleetgrpc "github.com/llm-d/fleet-llm-d/pkg/grpc"
)

// Run starts the fleet controller HTTP servers and blocks until the context
// is cancelled or a shutdown signal is received. When grpcPort is non-zero,
// a JSON-RPC listener is started alongside the REST API, exposing the
// FleetService defined in api/proto/fleet/v1/fleet.proto.
func (fc *FleetController) Run(ctx context.Context, port, metricsPort, grpcPort int, authCfg auth.Config, tlsCert, tlsKey, mode string, rateLimiter *auth.RateLimiter, rateLimitExempt []string) error {
	// Create a context that is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Wrap the API server mux with auth middleware and rate limiting.
	mux := fc.SetupRoutes(mode)
	exempt := defaultExemptPaths(rateLimitExempt)
	var handler http.Handler = auth.AuthorizationMiddleware(exempt, mux)
	handler = auth.AuthMiddleware(authCfg, exempt, handler)
	if rateLimiter != nil {
		handler = auth.RateLimitMiddlewareWithExemptions(rateLimiter, exempt, handler)
	}

	apiServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 180 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	metricsServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", metricsPort),
		Handler:      setupMetricsServer(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start gRPC (JSON-RPC) server when grpcPort is configured.
	if grpcPort > 0 {
		rpcSvc := fleetgrpc.NewFleetService(
			func() (interface{}, error) {
				return fc.ClusterClient.ListClusters(context.Background())
			},
			func() (interface{}, error) {
				if fc.Reconciler != nil {
					if pools := fc.Reconciler.ListPools(); len(pools) > 0 {
						return pools, nil
					}
				}
				return fc.PoolRepo.List(context.Background())
			},
		)
		rpcSvc.SetRegisterCluster(func(req fleetgrpc.RegisterClusterRequest) (*fleetgrpc.RegisterClusterResponse, error) {
			reg := client.ClusterRegistration{
				ID:     req.ID,
				Name:   req.Name,
				Region: req.Region,
				Labels: req.Labels,
			}
			reg, err := client.NormalizeClusterRegistration(reg)
			if err != nil {
				return nil, err
			}
			if err := fc.ClusterClient.RegisterCluster(context.Background(), reg); err != nil {
				return nil, err
			}
			clustersGauge.Add(1)
			return &fleetgrpc.RegisterClusterResponse{ID: reg.ID, Status: "registered"}, nil
		})

		grpcListener, err := fleetgrpc.Serve(fmt.Sprintf(":%d", grpcPort), rpcSvc)
		if err != nil {
			return fmt.Errorf("grpc server: %w", err)
		}
		defer grpcListener.Close()
		log.Printf("grpc server listening on :%d", grpcPort)
	}

	// Start metrics server.
	go func() {
		log.Printf("metrics server listening on :%d", metricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	// Start API server (with TLS if cert and key are provided).
	go func() {
		if tlsCert != "" && tlsKey != "" {
			log.Printf("api server listening on :%d (TLS enabled)", port)
			if err := apiServer.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
				log.Printf("api server error: %v", err)
			}
		} else {
			log.Printf("api server listening on :%d", port)
			if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("api server error: %v", err)
			}
		}
	}()

	// Start CRD and ModelPlane watchers only when running control plane.
	if mode != "inference" {
		// Start CRD watcher if Kubernetes API is configured.
		if fc.CRDWatcher != nil {
			if err := fc.CRDWatcher.Start(ctx); err != nil {
				log.Printf("WARNING: CRD watcher failed to start: %v", err)
			} else {
				log.Println("CRD watcher started for FleetInferencePool resources")
			}
		}

		// Start ModelPlane watcher if configured.
		if fc.ModelPlaneWatcher != nil {
			if err := fc.ModelPlaneWatcher.Start(ctx); err != nil {
				log.Printf("WARNING: ModelPlane watcher failed to start: %v", err)
			} else {
				log.Println("ModelPlane watcher started")
			}
		}
	}

	// Mark as ready.
	fc.ready.Store(true)
	log.Println("fleet-controller is ready")

	// Wait for shutdown signal.
	<-ctx.Done()
	log.Println("fleet-controller shutting down...")

	// Graceful shutdown with a 15-second deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var shutdownErr error
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		shutdownErr = fmt.Errorf("api server shutdown: %w", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		if shutdownErr != nil {
			shutdownErr = fmt.Errorf("%v; metrics server shutdown: %w", shutdownErr, err)
		} else {
			shutdownErr = fmt.Errorf("metrics server shutdown: %w", err)
		}
	}

	log.Println("fleet-controller stopped")
	return shutdownErr
}
