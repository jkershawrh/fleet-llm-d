package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListClusters_EmptyInitially(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var clusters []json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &clusters); err != nil {
		t.Fatalf("response is not a JSON array: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("expected empty cluster list, got %d", len(clusters))
	}
}

func TestRegisterCluster_Returns201(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	body := `{"name":"test-cluster","region":"us-east-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if resp["status"] != "registered" {
		t.Fatalf("expected status=registered, got %q", resp["status"])
	}
	if resp["id"] == "" {
		t.Fatal("expected non-empty id in response")
	}
}

func TestRegisterCluster_DuplicateReturns409(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	body := `{"name":"dup-cluster","region":"us-east-1"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	mux.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", rr1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d: %s", rr2.Code, rr2.Body.String())
	}
}

func TestRegisterCluster_EmptyNameReturns400(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	body := `{"name":"","region":"us-east-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}
