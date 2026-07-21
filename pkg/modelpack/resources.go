package modelpack

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// GPURequirements computed from model metadata.
type GPURequirements struct {
	MinGPUMemoryGB    float64
	RecommendedGPUs   int
	SupportedGPUTypes []string
	TensorParallelism int
	EstimatedThroughput float64 // tok/s estimate based on param size + precision
}

// gpuSpec describes a known GPU type and its memory capacity.
type gpuSpec struct {
	Name     string
	MemoryGB float64
}

// knownGPUs lists the standard GPU types in descending memory order.
var knownGPUs = []gpuSpec{
	{Name: "B200", MemoryGB: 192},
	{Name: "MI300X", MemoryGB: 192},
	{Name: "H200", MemoryGB: 141},
	{Name: "Gaudi3", MemoryGB: 128},
	{Name: "Gaudi2", MemoryGB: 96},
	{Name: "H100", MemoryGB: 80},
	{Name: "A100", MemoryGB: 80},
}

// ComputeGPURequirements estimates GPU needs from a ModelPack config.
//
// The estimation works as follows:
//  1. Parse paramSize (e.g. "70b" -> 70 billion parameters).
//  2. Use precision to determine bytes per parameter:
//     fp32=4, fp16/float16=2, bf16/bfloat16=2, int8=1, fp8=1, int4=0.5
//  3. Compute total model memory in GB (params * bytesPerParam / 1e9).
//     Add a 20% overhead for KV cache, activations, and runtime buffers.
//  4. Find the smallest set of GPUs that can host the model.
//  5. Estimate throughput based on param count and precision.
func ComputeGPURequirements(config *ModelPackConfig) (*GPURequirements, error) {
	if config == nil {
		return nil, fmt.Errorf("config must not be nil")
	}

	params, err := parseParamSize(config.Config.ParamSize)
	if err != nil {
		return nil, fmt.Errorf("parsing paramSize: %w", err)
	}

	bytesPerParam, err := bytesPerParamForPrecision(config.Config.Precision)
	if err != nil {
		return nil, fmt.Errorf("determining bytes per param: %w", err)
	}

	// Raw model weight memory in GB.
	rawMemoryGB := params * bytesPerParam / 1e9

	// Add 20% overhead for KV cache, activations, and runtime buffers.
	totalMemoryGB := rawMemoryGB * 1.2

	// Determine which GPUs can host the model and how many are needed.
	supported, recommendedCount, tp := selectGPUs(totalMemoryGB)

	// Rough throughput estimate: smaller models with lower precision are faster.
	throughput := estimateThroughput(params, bytesPerParam)

	return &GPURequirements{
		MinGPUMemoryGB:    math.Ceil(totalMemoryGB),
		RecommendedGPUs:   recommendedCount,
		SupportedGPUTypes: supported,
		TensorParallelism: tp,
		EstimatedThroughput: math.Round(throughput*10) / 10,
	}, nil
}

// parseParamSize converts a string like "70b" or "8B" to a float64
// representing billions of parameters (e.g. 70e9).
func parseParamSize(s string) (float64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty paramSize")
	}

	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "b"):
		multiplier = 1e9
		s = strings.TrimSuffix(s, "b")
	case strings.HasSuffix(s, "m"):
		multiplier = 1e6
		s = strings.TrimSuffix(s, "m")
	default:
		return 0, fmt.Errorf("unrecognized paramSize suffix in %q (expected 'b' or 'm')", s)
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing numeric part of paramSize %q: %w", s, err)
	}
	if val <= 0 {
		return 0, fmt.Errorf("paramSize must be positive, got %v", val)
	}

	return val * multiplier, nil
}

// bytesPerParamForPrecision returns the number of bytes per parameter for a
// given precision string.
func bytesPerParamForPrecision(precision string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(precision)) {
	case "fp32", "float32":
		return 4.0, nil
	case "fp16", "float16":
		return 2.0, nil
	case "bf16", "bfloat16":
		return 2.0, nil
	case "int8":
		return 1.0, nil
	case "fp8":
		return 1.0, nil
	case "int4":
		return 0.5, nil
	case "":
		// Default to fp16 when precision is unspecified.
		return 2.0, nil
	default:
		return 0, fmt.Errorf("unknown precision %q", precision)
	}
}

// selectGPUs determines which GPU types can host the model, how many of the
// best-fit GPU are recommended, and the tensor parallelism degree.
func selectGPUs(totalMemoryGB float64) (supported []string, recommendedCount int, tp int) {
	// Find all GPU types that could host the model (possibly multi-GPU).
	for _, gpu := range knownGPUs {
		gpuCount := int(math.Ceil(totalMemoryGB / gpu.MemoryGB))
		if gpuCount <= 8 { // practical upper limit for tensor parallelism
			supported = append(supported, gpu.Name)
		}
	}

	if len(supported) == 0 {
		// Fallback: require the largest GPU we know about.
		supported = []string{knownGPUs[0].Name}
	}

	// Recommend based on the first (largest-memory) supported GPU.
	bestGPUMem := 0.0
	for _, gpu := range knownGPUs {
		if gpu.Name == supported[0] {
			bestGPUMem = gpu.MemoryGB
			break
		}
	}

	recommendedCount = int(math.Ceil(totalMemoryGB / bestGPUMem))
	if recommendedCount < 1 {
		recommendedCount = 1
	}

	// Tensor parallelism equals the GPU count (1 = no parallelism needed).
	tp = recommendedCount

	return supported, recommendedCount, tp
}

// estimateThroughput provides a rough tok/s estimate. This is a simplified
// heuristic: throughput is inversely proportional to model size and bytes per
// parameter, with a baseline of ~100 tok/s for a 7B fp16 model on a single GPU.
func estimateThroughput(params float64, bytesPerParam float64) float64 {
	// Baseline: 7B params at fp16 (2 bytes) -> ~100 tok/s
	baselineParams := 7e9
	baselineBytes := 2.0
	baselineThroughput := 100.0

	// Throughput scales inversely with model memory footprint.
	ratio := (baselineParams * baselineBytes) / (params * bytesPerParam)
	return baselineThroughput * ratio
}
