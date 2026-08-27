package server

import "testing"

func TestParseInferenceProviderName(t *testing.T) {
	tests := []struct {
		input string
		want  InferenceProviderName
		err   bool
	}{
		{"", InferenceProviderPraxis, false},
		{"praxis", InferenceProviderPraxis, false},
		{"llm-d-router", InferenceProviderLLMD, false},
		{"other", "", true},
	}
	for _, tc := range tests {
		got, err := ParseInferenceProviderName(tc.input)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("ParseInferenceProviderName(%q) = %q, %v; want %q, err=%v", tc.input, got, err, tc.want, tc.err)
		}
	}
}

func TestInferenceTargetSelectsExactModelRouterPool(t *testing.T) {
	fc := &FleetController{
		InferenceProviderName: InferenceProviderLLMD,
		CPUPhysicalModel:      defaultCPUModel,
		LLMDCPUURL:            "http://router-cpu/",
		LLMDGPUURL:            "http://router-gpu/",
		LLMDToken:             "router-token",
	}
	cpu, err := fc.inferenceTarget(defaultCPUModel)
	if err != nil || cpu.BaseURL != "http://router-cpu" || cpu.APIToken != "router-token" {
		t.Fatalf("CPU target = %#v, %v", cpu, err)
	}
	gpu, err := fc.inferenceTarget(defaultGPUModel)
	if err != nil || gpu.BaseURL != "http://router-gpu" {
		t.Fatalf("GPU target = %#v, %v", gpu, err)
	}
}

func TestInferenceTargetFailsClosedWhenRouterPoolMissing(t *testing.T) {
	fc := &FleetController{InferenceProviderName: InferenceProviderLLMD}
	if _, err := fc.inferenceTarget(defaultGPUModel); err == nil {
		t.Fatal("expected missing Router endpoint to fail closed")
	}
}

func TestRouterClusterForUpstreamEnforcesExactModelPool(t *testing.T) {
	fc := &FleetController{RouterUpstreamClusters: map[string]string{
		"ovms.example:443": "arena-xeon6",
		"vllm.example:443": brutusGPUCluster,
	}}
	if cluster, err := fc.routerClusterForUpstream("HTTPS://OVMS.EXAMPLE:443", defaultCPUModel); err != nil || cluster != "arena-xeon6" {
		t.Fatalf("CPU evidence = %q, %v", cluster, err)
	}
	if _, err := fc.routerClusterForUpstream("vllm.example:443", defaultCPUModel); err == nil {
		t.Fatal("expected GPU upstream to be rejected for CPU model")
	}
}
