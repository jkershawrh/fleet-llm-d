package grpc

import (
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
)

// FleetService implements FleetServiceServer using callback functions that
// delegate to the fleet-controller's existing handler logic. This keeps the
// RPC layer thin and avoids duplicating business logic.
type FleetService struct {
	listClusters    func() (interface{}, error)
	listPools       func() (interface{}, error)
	registerCluster func(req RegisterClusterRequest) (*RegisterClusterResponse, error)
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
	result, err := s.listClusters()
	if err != nil {
		return err
	}
	*resp = result
	return nil
}

// ListPools handles the FleetService.ListPools RPC.
func (s *FleetService) ListPools(req *Empty, resp *interface{}) error {
	result, err := s.listPools()
	if err != nil {
		return err
	}
	*resp = result
	return nil
}

// RegisterCluster handles the FleetService.RegisterCluster RPC.
func (s *FleetService) RegisterCluster(req *RegisterClusterRequest, resp *RegisterClusterResponse) error {
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
	server := rpc.NewServer()
	if err := server.RegisterName("FleetService", svc); err != nil {
		return nil, fmt.Errorf("register FleetService: %w", err)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
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
