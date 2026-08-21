package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCostPricing_Returns200WithPrices(t *testing.T) {
	fc := newTestFleetController(t)
	mux := fc.SetupRoutes("control")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cost/pricing", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var prices interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &prices); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}
