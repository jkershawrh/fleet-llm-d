package server

import "testing"

func newTestFleetController(t *testing.T) *FleetController {
	t.Helper()
	fc, err := NewFleetController("", "http://vllm", "http://ovms", "", "")
	if err != nil {
		t.Fatalf("NewFleetController: %v", err)
	}
	return fc
}
