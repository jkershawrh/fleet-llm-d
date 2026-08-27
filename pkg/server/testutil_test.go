package server

import "testing"

const testGPUProvider = "gpu-provider"

func newTestFleetController(t *testing.T) *FleetController {
	t.Helper()
	fc, err := NewFleetController("", "http://vllm", "http://ovms", "", "")
	if err != nil {
		t.Fatalf("NewFleetController: %v", err)
	}
	fc.ModelProviderClusters = map[string][]string{
		defaultCPUModel: {"cluster-b", "cpu-provider-b", "cpu-provider-a"},
		defaultGPUModel: {testGPUProvider},
	}
	return fc
}
