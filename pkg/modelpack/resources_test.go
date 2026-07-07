package modelpack

import (
	"math"
	"testing"
)

func TestComputeGPURequirements_Small(t *testing.T) {
	config := &ModelPackConfig{
		Descriptor: ModelDescriptor{
			Name:   "phi-3-mini",
			Family: "phi",
			Vendor: "microsoft",
		},
		Config: ModelTechnicalConfig{
			Architecture: "transformer",
			Format:       "safetensors",
			ParamSize:    "8b",
			Precision:    "fp16",
		},
		OciRef: "registry.example.com/models/phi-3-mini:v1",
	}

	req, err := ComputeGPURequirements(config)
	if err != nil {
		t.Fatalf("ComputeGPURequirements() error: %v", err)
	}

	// 8B params * 2 bytes = 16GB raw, +20% overhead = 19.2GB -> ceil = 20GB
	if req.MinGPUMemoryGB != 20 {
		t.Errorf("MinGPUMemoryGB = %v, want 20", req.MinGPUMemoryGB)
	}

	// Should fit on a single GPU (80GB or larger).
	if req.RecommendedGPUs != 1 {
		t.Errorf("RecommendedGPUs = %d, want 1", req.RecommendedGPUs)
	}

	// Should support all GPU types since 20GB fits on anything.
	if len(req.SupportedGPUTypes) == 0 {
		t.Error("expected at least one supported GPU type")
	}

	// Single GPU means no tensor parallelism.
	if req.TensorParallelism != 1 {
		t.Errorf("TensorParallelism = %d, want 1", req.TensorParallelism)
	}

	// Throughput should be positive.
	if req.EstimatedThroughput <= 0 {
		t.Errorf("EstimatedThroughput = %v, want > 0", req.EstimatedThroughput)
	}
}

func TestComputeGPURequirements_Large(t *testing.T) {
	config := &ModelPackConfig{
		Descriptor: ModelDescriptor{
			Name:   "llama-3-70b",
			Family: "llama3",
			Vendor: "meta",
		},
		Config: ModelTechnicalConfig{
			Architecture: "transformer",
			Format:       "safetensors",
			ParamSize:    "70b",
			Precision:    "bf16",
		},
		OciRef: "registry.example.com/models/llama-3-70b:v1",
	}

	req, err := ComputeGPURequirements(config)
	if err != nil {
		t.Fatalf("ComputeGPURequirements() error: %v", err)
	}

	// 70B * 2 bytes = 140GB raw, +20% = 168GB -> ceil = 168GB
	if req.MinGPUMemoryGB != 168 {
		t.Errorf("MinGPUMemoryGB = %v, want 168", req.MinGPUMemoryGB)
	}

	// At 168GB, needs at least 2 H100s (80GB each) or 1 B200/MI300X (192GB).
	// The first supported GPU is B200 (192GB), so recommended = 1.
	if req.RecommendedGPUs < 1 {
		t.Errorf("RecommendedGPUs = %d, want >= 1", req.RecommendedGPUs)
	}

	if len(req.SupportedGPUTypes) == 0 {
		t.Error("expected at least one supported GPU type")
	}

	// Throughput should be lower than the 8B model.
	if req.EstimatedThroughput <= 0 {
		t.Errorf("EstimatedThroughput = %v, want > 0", req.EstimatedThroughput)
	}
}

func TestComputeGPURequirements_Quantized(t *testing.T) {
	config := &ModelPackConfig{
		Descriptor: ModelDescriptor{
			Name:   "llama-3-70b-awq",
			Family: "llama3",
			Vendor: "meta",
		},
		Config: ModelTechnicalConfig{
			Architecture: "transformer",
			Format:       "safetensors",
			ParamSize:    "70b",
			Precision:    "int4",
			Quantization: "awq",
		},
		OciRef: "registry.example.com/models/llama-3-70b-awq:v1",
	}

	req, err := ComputeGPURequirements(config)
	if err != nil {
		t.Fatalf("ComputeGPURequirements() error: %v", err)
	}

	// 70B * 0.5 bytes = 35GB raw, +20% = 42GB -> ceil = 42GB
	if req.MinGPUMemoryGB != 42 {
		t.Errorf("MinGPUMemoryGB = %v, want 42", req.MinGPUMemoryGB)
	}

	// 42GB fits on a single 80GB GPU.
	if req.RecommendedGPUs != 1 {
		t.Errorf("RecommendedGPUs = %d, want 1", req.RecommendedGPUs)
	}

	// Quantized model should need significantly less memory than fp16.
	fp16Config := &ModelPackConfig{
		Config: ModelTechnicalConfig{
			ParamSize: "70b",
			Precision: "fp16",
		},
	}
	fp16Req, err := ComputeGPURequirements(fp16Config)
	if err != nil {
		t.Fatalf("ComputeGPURequirements(fp16) error: %v", err)
	}

	if req.MinGPUMemoryGB >= fp16Req.MinGPUMemoryGB {
		t.Errorf("quantized model memory (%vGB) should be less than fp16 (%vGB)",
			req.MinGPUMemoryGB, fp16Req.MinGPUMemoryGB)
	}

	// Quantized model should have higher throughput than fp16 equivalent.
	if req.EstimatedThroughput <= fp16Req.EstimatedThroughput {
		t.Errorf("quantized throughput (%.1f) should exceed fp16 (%.1f)",
			req.EstimatedThroughput, fp16Req.EstimatedThroughput)
	}
}

func TestComputeGPURequirements_NilConfig(t *testing.T) {
	_, err := ComputeGPURequirements(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestParseParamSize(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"8b", 8e9},
		{"70B", 70e9},
		{"120b", 120e9},
		{"7.5b", 7.5e9},
		{"350m", 350e6},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseParamSize(tt.input)
			if err != nil {
				t.Fatalf("parseParamSize(%q) error: %v", tt.input, err)
			}
			if math.Abs(got-tt.want) > 1 {
				t.Errorf("parseParamSize(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
