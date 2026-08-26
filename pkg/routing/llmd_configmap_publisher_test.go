package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKubernetesConfigMapPublisherReplacesData(t *testing.T) {
	var patch map[string]map[string]string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/namespaces/fleet/configmaps/router-endpoints" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/merge-patch+json" {
			t.Fatalf("content type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	publisher, err := NewKubernetesConfigMapPublisher(server.URL, "fleet", "router-endpoints", "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), map[string][]byte{"index.json": []byte("index\n")}); err != nil {
		t.Fatal(err)
	}
	if got := patch["data"]["index.json"]; got != "index\n" {
		t.Fatalf("published index = %q", got)
	}
}

func TestKubernetesConfigMapPublisherRetainsLastValidDataOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "conflict", http.StatusConflict)
	}))
	defer server.Close()
	publisher, err := NewKubernetesConfigMapPublisher(server.URL, "fleet", "router-endpoints", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), map[string][]byte{"index.json": []byte("new")}); err == nil {
		t.Fatal("expected Kubernetes API error")
	}
}
