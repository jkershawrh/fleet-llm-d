package routing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLLMDProviderFiltersAndSeparatesExactModels(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	p, err := NewLLMDProvider(LLMDProviderOptions{Directory: dir, Namespace: "fleet", RequireTLS: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	clusters := []FleetClusterInfo{
		{ID: "oberon", Region: "central", Status: "Running", UpdatedAt: now, Authorized: true, EgressAddress: "https://oberon.example:443", MetricsEndpoint: "https://metrics.oberon.example:9443"},
		{ID: "arena", Status: "draining", UpdatedAt: now, Authorized: true, EgressAddress: "https://arena.example"},
		{ID: "brutus", Status: "Running", UpdatedAt: now.Add(-time.Minute), Authorized: true, EgressAddress: "https://brutus.example"},
	}
	pools := []FleetPoolInfo{
		{Name: "cpu", ModelName: "logical", PhysicalModel: "granite-cpu", Clusters: []string{"oberon", "arena"}},
		{Name: "gpu", ModelName: "logical", PhysicalModel: "granite-gpu", Clusters: []string{"brutus"}},
	}
	if err := p.Sync(context.Background(), clusters, pools); err != nil {
		t.Fatal(err)
	}

	var index llmdModelIndex
	readJSON(t, filepath.Join(dir, "index.json"), &index)
	if len(index.Models) != 1 || index.Models["granite-cpu"] == "" {
		t.Fatalf("index models = %#v", index.Models)
	}
	var endpoints llmdEndpointsFile
	readJSON(t, filepath.Join(dir, index.Models["granite-cpu"]), &endpoints)
	if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].Name != "oberon--cpu" {
		t.Fatalf("endpoints = %#v", endpoints.Endpoints)
	}
	if got := endpoints.Endpoints[0].Labels["metricsAddress"]; got != "metrics.oberon.example" {
		t.Fatalf("metricsAddress = %q", got)
	}
	if got := endpoints.Endpoints[0].Labels["model"]; got != "granite-cpu" {
		t.Fatalf("model = %q", got)
	}
}

func TestLLMDProviderRetainsLastValidFilesOnFailure(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewLLMDProvider(LLMDProviderOptions{Directory: dir, RequireTLS: true})
	goodClusters := []FleetClusterInfo{{ID: "a", Status: "Running", Authorized: true, EgressAddress: "https://a.example"}}
	pools := []FleetPoolInfo{{Name: "pool", ModelName: "model", Clusters: []string{"a"}}}
	if err := p.Sync(context.Background(), goodClusters, pools); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	bad := []FleetClusterInfo{{ID: "a", Status: "Running", Authorized: true, EgressAddress: "http://a.example"}}
	if err := p.Sync(context.Background(), bad, pools); err == nil {
		t.Fatal("expected TLS validation error")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if string(after) != string(before) {
		t.Fatal("last valid index changed after failed generation")
	}
}

func TestLLMDProviderOutputIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewLLMDProvider(LLMDProviderOptions{Directory: dir, RequireTLS: true})
	clusters := []FleetClusterInfo{
		{ID: "b", Status: "Running", Authorized: true, EgressAddress: "https://b.example"},
		{ID: "a", Status: "Running", Authorized: true, EgressAddress: "https://a.example"},
	}
	pools := []FleetPoolInfo{{Name: "pool", ModelName: "model", Clusters: []string{"b", "a"}}}
	if err := p.Sync(context.Background(), clusters, pools); err != nil {
		t.Fatal(err)
	}
	var index llmdModelIndex
	readJSON(t, filepath.Join(dir, "index.json"), &index)
	first, _ := os.ReadFile(filepath.Join(dir, index.Models["model"]))
	if err := p.Sync(context.Background(), []FleetClusterInfo{clusters[1], clusters[0]}, pools); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, index.Models["model"]))
	if string(first) != string(second) {
		t.Fatal("endpoint output is not deterministic")
	}
}

func readJSON(t *testing.T, path string, out interface{}) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
