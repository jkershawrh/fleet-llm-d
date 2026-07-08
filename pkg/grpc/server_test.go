package grpc

import (
	"net/rpc/jsonrpc"
	"testing"
)

func TestFleetService_ListClusters(t *testing.T) {
	svc := NewFleetService(
		func() (interface{}, error) {
			return []map[string]string{{"id": "c1", "name": "test-cluster"}}, nil
		},
		func() (interface{}, error) {
			return nil, nil
		},
	)

	listener, err := Serve(":0", svc)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	conn, err := jsonrpc.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var result interface{}
	err = conn.Call("FleetService.ListClusters", &Empty{}, &result)
	if err != nil {
		t.Fatalf("ListClusters RPC failed: %v", err)
	}

	clusters, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
}

func TestFleetService_ListPools(t *testing.T) {
	svc := NewFleetService(
		func() (interface{}, error) {
			return nil, nil
		},
		func() (interface{}, error) {
			return []map[string]string{
				{"name": "granite-pool", "model": "granite-3.3-2b", "phase": "Running"},
			}, nil
		},
	)

	listener, err := Serve(":0", svc)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	conn, err := jsonrpc.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var result interface{}
	err = conn.Call("FleetService.ListPools", &Empty{}, &result)
	if err != nil {
		t.Fatalf("ListPools RPC failed: %v", err)
	}

	pools, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
}

func TestFleetService_RegisterCluster(t *testing.T) {
	registered := false
	svc := NewFleetService(
		func() (interface{}, error) {
			if registered {
				return []map[string]string{{"id": "new-1", "name": "new-cluster"}}, nil
			}
			return []map[string]string{}, nil
		},
		func() (interface{}, error) {
			return nil, nil
		},
	)
	svc.SetRegisterCluster(func(req RegisterClusterRequest) (*RegisterClusterResponse, error) {
		registered = true
		return &RegisterClusterResponse{ID: req.ID, Status: "registered"}, nil
	})

	listener, err := Serve(":0", svc)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	conn, err := jsonrpc.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Register a new cluster.
	regReq := RegisterClusterRequest{ID: "new-1", Name: "new-cluster", Region: "us-east-1"}
	var regResp RegisterClusterResponse
	err = conn.Call("FleetService.RegisterCluster", &regReq, &regResp)
	if err != nil {
		t.Fatalf("RegisterCluster RPC failed: %v", err)
	}
	if regResp.ID != "new-1" {
		t.Errorf("expected id 'new-1', got %q", regResp.ID)
	}
	if regResp.Status != "registered" {
		t.Errorf("expected status 'registered', got %q", regResp.Status)
	}

	// Verify the cluster appears in ListClusters.
	var listResult interface{}
	err = conn.Call("FleetService.ListClusters", &Empty{}, &listResult)
	if err != nil {
		t.Fatalf("ListClusters after register failed: %v", err)
	}
	clusters, ok := listResult.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", listResult)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster after registration, got %d", len(clusters))
	}
}

func TestServe_InvalidAddress(t *testing.T) {
	svc := NewFleetService(
		func() (interface{}, error) { return nil, nil },
		func() (interface{}, error) { return nil, nil },
	)

	// Bind to a specific port first, then try to bind again to get an error.
	listener1, err := Serve(":0", svc)
	if err != nil {
		t.Fatal(err)
	}
	defer listener1.Close()

	// Try to bind to the same address — should fail.
	_, err = Serve(listener1.Addr().String(), svc)
	if err == nil {
		t.Fatal("expected error when binding to already-bound address")
	}
}
