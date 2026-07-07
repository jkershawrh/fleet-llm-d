package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Backend represents an inference backend endpoint.
type Backend struct {
	Name      string  // e.g., "granite-vllm-pool", "granite-ovms-pool"
	URL       string  // e.g., "http://vllm-cpu.fleet-llm-d.svc:8000"
	Runtime   string  // "vllm" or "ovms"
	Healthy   bool
	LatencyMs float64
}

// InferenceProxy routes inference requests to backend model servers.
type InferenceProxy struct {
	mu       sync.RWMutex
	backends map[string][]Backend // model -> backends
	rrIndex  atomic.Uint64        // round-robin counter
	http     *http.Client
}

// NewInferenceProxy creates a new InferenceProxy with an HTTP client
// configured for proxying inference requests.
func NewInferenceProxy() *InferenceProxy {
	return &InferenceProxy{
		backends: make(map[string][]Backend),
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// RegisterBackend adds a backend for a model.
func (p *InferenceProxy) RegisterBackend(model string, backend Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.backends[model] = append(p.backends[model], backend)
}

// SelectBackend picks the best backend for a request based on headers.
// Returns (backend, reason, error).
//
// Logic:
//   - If x-llm-d-inference-objective: "realtime" -> pick lowest latency healthy backend
//   - If x-llm-d-inference-objective: "batch" -> pick any healthy backend (prefer cheapest)
//   - Default: round-robin among healthy backends
func (p *InferenceProxy) SelectBackend(model string, headers http.Header) (*Backend, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	backends, ok := p.backends[model]
	if !ok || len(backends) == 0 {
		return nil, "", fmt.Errorf("no backends registered for model %q", model)
	}

	// Filter to healthy backends.
	var healthy []Backend
	for _, b := range backends {
		if b.Healthy {
			healthy = append(healthy, b)
		}
	}
	if len(healthy) == 0 {
		return nil, "", fmt.Errorf("no healthy backends for model %q", model)
	}

	objective := headers.Get("x-llm-d-inference-objective")

	switch objective {
	case "realtime":
		best := healthy[0]
		for _, b := range healthy[1:] {
			if b.LatencyMs < best.LatencyMs {
				best = b
			}
		}
		return &best, "realtime: lowest latency", nil

	case "batch":
		// Prefer the first healthy backend (cheapest by convention).
		return &healthy[0], "batch: cost-optimized", nil

	default:
		// Round-robin among healthy backends.
		idx := p.rrIndex.Add(1) - 1
		selected := healthy[idx%uint64(len(healthy))]
		return &selected, "default: round-robin", nil
	}
}

// ProxyRequest forwards an inference request to the selected backend.
// It copies the request body, sends it to the backend, and streams the response back.
func (p *InferenceProxy) ProxyRequest(w http.ResponseWriter, r *http.Request, backend *Backend) {
	// Read the original body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadGateway)
		return
	}

	// Build the backend request, preserving the original path.
	backendURL := backend.URL + r.URL.Path
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, backendURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"failed to create proxy request"}`, http.StatusBadGateway)
		return
	}

	// Copy relevant headers.
	for key, vals := range r.Header {
		for _, v := range vals {
			proxyReq.Header.Add(key, v)
		}
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(proxyReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"backend request failed: %s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}

	// Inject fleet routing headers.
	w.Header().Set("X-Fleet-Routed-To", backend.Name)

	w.WriteHeader(resp.StatusCode)

	// Stream the response body back.
	io.Copy(w, resp.Body)
}

// inferenceRequest is a minimal struct to extract the model from OpenAI-compatible
// request bodies.
type inferenceRequest struct {
	Model string `json:"model"`
}

// ServeHTTP handles /v1/chat/completions and /v1/completions.
func (p *InferenceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Read the body so we can peek at the model field.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}

	// Parse the model from the request.
	var req inferenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		http.Error(w, `{"error":"model field is required"}`, http.StatusBadRequest)
		return
	}

	// Select a backend.
	backend, reason, err := p.SelectBackend(req.Model, r.Header)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}

	// Restore the body for the proxy request.
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Inject routing reason header in response.
	w.Header().Set("X-Fleet-Routing-Reason", reason)

	p.ProxyRequest(w, r, backend)
}
