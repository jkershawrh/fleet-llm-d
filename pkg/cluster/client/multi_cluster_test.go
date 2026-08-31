package client

import (
	"context"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/placement/solver"
)

func resetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]ClusterRegistration)
}

func TestRegisterCluster(t *testing.T) {
	resetRegistryForTest()
	tests := []struct {
		name    string
		reg     ClusterRegistration
		wantErr bool
	}{
		{
			name: "valid registration",
			reg: ClusterRegistration{
				ID:             "cluster-1",
				Name:           "us-east-prod",
				Region:         "us-east-1",
				KubeconfigPath: "/etc/kube/config",
				Labels: map[string]string{
					"env": "production",
				},
			},
			wantErr: false,
		},
	}

	client := NewMultiClusterClient()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.RegisterCluster(context.Background(), tt.reg)
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterCluster() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterCluster_GeneratesStableIDFromName(t *testing.T) {
	resetRegistryForTest()
	client := NewMultiClusterClient()

	err := client.RegisterCluster(context.Background(), ClusterRegistration{
		Name:   "US East Production",
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("RegisterCluster() unexpected error: %v", err)
	}

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters() error: %v", err)
	}

	found := false
	for _, cluster := range clusters {
		if cluster.Name == "US East Production" {
			found = true
			if cluster.ID != "us-east-production" {
				t.Fatalf("expected generated stable ID %q, got %q", "us-east-production", cluster.ID)
			}
		}
	}
	if !found {
		t.Fatal("expected generated-ID cluster to be registered")
	}
}

func TestListClusters(t *testing.T) {
	resetRegistryForTest()
	tests := []struct {
		name    string
		want    []solver.ClusterInfo
		wantErr bool
	}{
		{
			name: "list all registered clusters",
			want: []solver.ClusterInfo{
				{
					ID:     "cluster-1",
					Name:   "us-east-prod",
					Region: "us-east-1",
					Labels: map[string]string{
						"env": "production",
					},
				},
			},
			wantErr: false,
		},
	}

	client := NewMultiClusterClient()
	err := client.RegisterCluster(context.Background(), ClusterRegistration{
		ID:     "cluster-1",
		Name:   "us-east-prod",
		Region: "us-east-1",
		Labels: map[string]string{
			"env": "production",
		},
	})
	if err != nil {
		t.Fatalf("RegisterCluster() setup error: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.ListClusters(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("ListClusters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ListClusters() returned %d clusters, want %d", len(got), len(tt.want))
			}
		})
	}
}

func TestGetClusterClient_NotFound(t *testing.T) {
	resetRegistryForTest()
	tests := []struct {
		name      string
		clusterID string
		wantErr   bool
	}{
		{
			name:      "non-existent cluster",
			clusterID: "no-such-cluster",
			wantErr:   true,
		},
	}

	client := NewMultiClusterClient()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.GetClusterClient(context.Background(), tt.clusterID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetClusterClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Errorf("GetClusterClient() returned nil client")
			}
		})
	}
}
