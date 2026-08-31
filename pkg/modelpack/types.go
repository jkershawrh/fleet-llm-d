package modelpack

// ModelPackConfig represents a parsed ModelPack OCI config following the
// CNCF model-spec (application/vnd.cncf.model.config.v1+json).
type ModelPackConfig struct {
	Descriptor ModelDescriptor
	Config     ModelTechnicalConfig
	OciRef     string // e.g., "registry.example.com/models/llama-3-70b:v1"
}

// ModelDescriptor captures the vendor-neutral identity of a model.
type ModelDescriptor struct {
	Name    string
	Family  string
	Version string
	Vendor  string
	License string
}

// ModelTechnicalConfig describes the architecture, format, and capabilities
// of a model as declared in a ModelPack manifest.
type ModelTechnicalConfig struct {
	Architecture string   // transformer, cnn, rnn
	Format       string   // safetensors, gguf, onnx, pt
	ParamSize    string   // 8b, 70b, 120b
	Precision    string   // float16, bfloat16, int8
	Quantization string   // awq, gptq, "" (empty when unquantized)
	InputTypes   []string // text, image, audio
	OutputTypes  []string
	ToolUsage    bool
	Reasoning    bool
	Languages    []string
}
