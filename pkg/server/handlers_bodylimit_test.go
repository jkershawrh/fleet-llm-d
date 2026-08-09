package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterCluster_RejectsOversizeBody(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	mux := fc.SetupRoutes("control")

	oversized := `{"name":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader([]byte(oversized)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusCreated || rr.Code == http.StatusOK {
		t.Fatalf("expected oversize body to be rejected, got %d", rr.Code)
	}
}

func TestRegisterCluster_AcceptsNormalBody(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	mux := fc.SetupRoutes("control")

	body := `{"name":"test-cluster","region":"us-east-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for normal body, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateRollout_RejectsOversizeBody(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	mux := fc.SetupRoutes("control")

	oversized := `{"pool_id":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rollouts", bytes.NewReader([]byte(oversized)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusCreated || rr.Code == http.StatusOK {
		t.Fatalf("expected oversize body to be rejected, got %d", rr.Code)
	}
}

func TestHandleIntentV1_RejectsOversizeBody(t *testing.T) {
	fc := NewFleetController("", "http://vllm", "http://ovms", "", "")
	mux := fc.SetupRoutes("control")

	oversized := `{"action":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intents", bytes.NewReader([]byte(oversized)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusCreated || rr.Code == http.StatusOK {
		t.Fatalf("expected oversize body to be rejected, got %d", rr.Code)
	}
}
