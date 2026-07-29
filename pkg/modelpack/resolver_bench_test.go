package modelpack

import "testing"

func benchmarkComputeGPURequirements(b *testing.B, paramSize, precision string) {
	config := &ModelPackConfig{
		Descriptor: ModelDescriptor{
			Name:   "bench-model",
			Family: "granite",
		},
		Config: ModelTechnicalConfig{
			Architecture: "transformer",
			Format:       "safetensors",
			ParamSize:    paramSize,
			Precision:    precision,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ComputeGPURequirements(config)
	}
}

func BenchmarkComputeGPURequirements_2B(b *testing.B)  { benchmarkComputeGPURequirements(b, "2b", "float16") }
func BenchmarkComputeGPURequirements_7B(b *testing.B)  { benchmarkComputeGPURequirements(b, "7b", "float16") }
func BenchmarkComputeGPURequirements_70B(b *testing.B) { benchmarkComputeGPURequirements(b, "70b", "bfloat16") }
func BenchmarkComputeGPURequirements_405B(b *testing.B) {
	benchmarkComputeGPURequirements(b, "405b", "int8")
}
