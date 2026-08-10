package grpc

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"

	"github.com/llm-d/fleet-llm-d/pkg/auth"
)

// FleetService implements FleetServiceServer using callback functions that
// delegate to the fleet-controller's existing handler logic. This keeps the
// RPC layer thin and avoids duplicating business logic.
type FleetService struct {
	listClusters    func() (interface{}, error)
	listPools       func() (interface{}, error)
	registerCluster func(req RegisterClusterRequest) (*RegisterClusterResponse, error)
	authSecret      string
}

// SetAuthSecret enables the same signed bearer-token validation used by the
// REST API. Controllers refuse to expose JSON-RPC unless this is configured.
func (s *FleetService) SetAuthSecret(secret string) {
	s.authSecret = secret
}

func (s *FleetService) authorize(token string, allowedRoles ...string) error {
	if s.authSecret == "" {
		return nil // direct library use remains available for isolated tests
	}
	claims, err := auth.ValidateToken(s.authSecret, token)
	if err != nil {
		return fmt.Errorf("unauthorized: %w", err)
	}
	for _, role := range allowedRoles {
		if claims.Role == role {
			return nil
		}
	}
	return fmt.Errorf("forbidden: role %q is not allowed", claims.Role)
}

// NewFleetService creates a FleetService wired to the given data callbacks.
// registerCluster is optional and can be set later via SetRegisterCluster.
func NewFleetService(
	listClusters func() (interface{}, error),
	listPools func() (interface{}, error),
) *FleetService {
	return &FleetService{
		listClusters: listClusters,
		listPools:    listPools,
	}
}

// SetRegisterCluster wires the RegisterCluster RPC handler.
func (s *FleetService) SetRegisterCluster(fn func(req RegisterClusterRequest) (*RegisterClusterResponse, error)) {
	s.registerCluster = fn
}

// ListClusters handles the FleetService.ListClusters RPC.
func (s *FleetService) ListClusters(req *Empty, resp *interface{}) error {
	if err := s.authorize(req.Token, "viewer", "operator", "admin"); err != nil {
		return err
	}
	result, err := s.listClusters()
	if err != nil {
		return err
	}
	*resp = result
	return nil
}

// ListPools handles the FleetService.ListPools RPC.
func (s *FleetService) ListPools(req *Empty, resp *interface{}) error {
	if err := s.authorize(req.Token, "viewer", "operator", "admin"); err != nil {
		return err
	}
	result, err := s.listPools()
	if err != nil {
		return err
	}
	*resp = result
	return nil
}

// RegisterCluster handles the FleetService.RegisterCluster RPC.
func (s *FleetService) RegisterCluster(req *RegisterClusterRequest, resp *RegisterClusterResponse) error {
	if err := s.authorize(req.Token, "operator", "admin"); err != nil {
		return err
	}
	if s.registerCluster == nil {
		return fmt.Errorf("RegisterCluster not implemented")
	}
	result, err := s.registerCluster(*req)
	if err != nil {
		return err
	}
	*resp = *result
	return nil
}

// Serve starts a JSON-RPC server on the given address and returns the
// listener. The caller is responsible for closing the listener to stop the
// server. The address ":0" picks an available port.
func Serve(addr string, svc *FleetService) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return serveListener(listener, svc)
}

// ServeTLS starts a TLS 1.3 JSON-RPC listener.
func ServeTLS(addr string, svc *FleetService, certFile, keyFile string) (net.Listener, error) {
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load JSON-RPC TLS certificate: %w", err)
	}
	listener, err := tls.Listen("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return serveListener(listener, svc)
}

func serveListener(listener net.Listener, svc *FleetService) (net.Listener, error) {
	server := rpc.NewServer()
	if err := server.RegisterName("FleetService", svc); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("register FleetService: %w", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go server.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()

	return listener, nil
}
