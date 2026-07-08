// Package grpc provides a JSON-RPC server that exposes the FleetService
// defined in api/proto/fleet/v1/fleet.proto. The types below are hand-written
// stubs that mirror the protobuf messages; they will be replaced by
// protoc-generated code once the build toolchain includes protoc.
//
// Generated from: api/proto/fleet/v1/fleet.proto
package grpc

// ---------------------------------------------------------------------------
// Request / response types — mirrors of the proto messages.
// ---------------------------------------------------------------------------

// Empty is the zero-value request for RPCs that take no parameters.
type Empty struct{}

// ClusterInfo represents a cluster registered with the fleet.
type ClusterInfo struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels,omitempty"`
}

// RegisterClusterRequest maps to fleet.v1.RegisterClusterRequest.
type RegisterClusterRequest struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Labels map[string]string `json:"labels,omitempty"`
}

// RegisterClusterResponse maps to fleet.v1.RegisterClusterResponse.
type RegisterClusterResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ListClustersRequest maps to fleet.v1.ListClustersRequest.
type ListClustersRequest = Empty

// PoolInfo represents an inference pool summary.
type PoolInfo struct {
	Name  string `json:"name"`
	Model string `json:"model"`
	Phase string `json:"phase"`
}

// FleetServiceServer defines the gRPC service interface for fleet operations.
// This interface matches the RPCs declared in api/proto/fleet/v1/fleet.proto.
type FleetServiceServer interface {
	ListClusters(req *Empty, resp *interface{}) error
	RegisterCluster(req *RegisterClusterRequest, resp *RegisterClusterResponse) error
	ListPools(req *Empty, resp *interface{}) error
}
