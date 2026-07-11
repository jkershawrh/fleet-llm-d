package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// proxyStripHeaders lists headers that must not be forwarded to inference
// backends.  This prevents leaking client credentials and removes hop-by-hop
// headers that are meaningless on the backend leg.
var proxyStripHeaders = map[string]bool{
	"Authorization":       true,
	"Cookie":              true,
	"Proxy-Authorization": true,
	"Connection":          true,
	"Transfer-Encoding":   true,
	"Keep-Alive":          true,
	"Trailer":             true,
	"Upgrade":             true,
	"Te":                  true,
}

// writeProxyError writes a JSON error response with the given HTTP status code
// and message.  Using json.Encoder guarantees proper escaping and prevents
// injection through attacker-controlled error strings.
func writeProxyError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Backend represents an inference backend endpoint.
type Backend struct {
	Name      string  // e.g., "granite-vllm-pool", "granite-ovms-pool"
	URL       string  // e.g., "http://vllm-cpu.fleet-llm-d.svc:8000"
	Runtime   string  // "vllm" or "ovms"
	PathPrefix string // API path prefix override (e.g., "/v3" for OVMS, "" for vLLM default /v1)
	Healthy   bool
	LatencyMs float64
}

// InferenceProxy routes inference requests to backend model servers.
type InferenceProxy struct {
	mu              sync.RWMutex
	backends        map[string][]Backend // model -> backends
	rrIndex         atomic.Uint64        // round-robin counter
	http            *http.Client
	inflight        sync.Map // model -> *int64 (atomic counter per model)
	maxInflight     int      // per-model max concurrent requests (0 = disabled)
	SemanticRouter  *SemanticRouter // optional semantic routing
}

// NewInferenceProxy creates a new InferenceProxy with an HTTP client
// configured for proxying inference requests.
func NewInferenceProxy() *InferenceProxy {
	return &InferenceProxy{
		backends: make(map[string][]Backend),
		http: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          200,
				MaxIdleConnsPerHost:   50,
				MaxConnsPerHost:       100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 120 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
			},
		},
	}
}

// RegisterBackend adds a backend for a model.
func (p *InferenceProxy) RegisterBackend(model string, backend Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.backends[model] = append(p.backends[model], backend)
}

// incrementInflight atomically increments the in-flight counter for a model
// and returns the new value.
func (p *InferenceProxy) incrementInflight(model string) int64 {
	counter, _ := p.inflight.LoadOrStore(model, new(int64))
	return atomic.AddInt64(counter.(*int64), 1)
}

// decrementInflight atomically decrements the in-flight counter for a model.
func (p *InferenceProxy) decrementInflight(model string) {
	if counter, ok := p.inflight.Load(model); ok {
		atomic.AddInt64(counter.(*int64), -1)
	}
}

// SetMaxInflight sets the per-model maximum concurrent in-flight requests.
// When the limit is exceeded, new requests are rejected with 503. A value
// of 0 disables load shedding (the default).
func (p *InferenceProxy) SetMaxInflight(max int) {
	p.maxInflight = max
}

// UpdateBackendHealth sets the health status of a named backend for a model.
func (p *InferenceProxy) UpdateBackendHealth(model, backendName string, healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	backends, ok := p.backends[model]
	if !ok {
		return
	}
	for i, b := range backends {
		if b.Name == backendName {
			backends[i].Healthy = healthy
			break
		}
	}
}

// StartHealthChecks begins periodic health polling of all registered backends.
// Each tick, it sends a GET to <backend>/v1/models and marks the backend
// healthy (200) or unhealthy (anything else / error). Polling stops when ctx
// is cancelled.
func (p *InferenceProxy) StartHealthChecks(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.checkAllBackends()
			}
		}
	}()
}

func (p *InferenceProxy) checkAllBackends() {
	p.mu.RLock()
	var checks []struct {
		model string
		name  string
		url   string
	}
	for model, backends := range p.backends {
		for _, b := range backends {
			checks = append(checks, struct {
				model string
				name  string
				url   string
			}{model, b.Name, b.URL})
		}
	}
	p.mu.RUnlock()

	client := &http.Client{Timeout: 5 * time.Second}
	for _, c := range checks {
		resp, err := client.Get(c.url + "/v1/models")
		healthy := err == nil && resp != nil && resp.StatusCode == 200
		if resp != nil {
			resp.Body.Close()
		}
		p.UpdateBackendHealth(c.model, c.name, healthy)
	}
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
// If the backend responds with Content-Type text/event-stream (SSE), the
// response is streamed with per-chunk flushing so that clients receive
// tokens in real-time. Non-streaming responses are copied in bulk.
func (p *InferenceProxy) ProxyRequest(w http.ResponseWriter, r *http.Request, backend *Backend) {
	// Read the original body.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "failed to read request body")
		return
	}

	// Build the backend request, preserving the original path.
	path := r.URL.Path
	if backend.PathPrefix != "" {
		path = strings.Replace(path, "/v1/", backend.PathPrefix+"/", 1)
	}
	backendURL := backend.URL + path
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, backendURL, bytes.NewReader(body))
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "failed to create proxy request")
		return
	}

	// Copy relevant headers, stripping auth and hop-by-hop headers.
	for key, vals := range r.Header {
		if proxyStripHeaders[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, v := range vals {
			proxyReq.Header.Add(key, v)
		}
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(proxyReq)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "backend request failed: "+err.Error())
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

	// Check if the backend is sending an SSE stream.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		// SSE streaming: flush each chunk immediately so the client
		// receives tokens as they are generated.
		flusher, ok := w.(http.Flusher)
		if !ok {
			// ResponseWriter does not support flushing — fall back to
			// a bulk copy (the client will see the full response at once).
			io.Copy(w, resp.Body)
			return
		}
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				flusher.Flush()
			}
			if readErr != nil {
				break
			}
		}
	} else {
		// Non-streaming: copy entire response body.
		io.Copy(w, resp.Body)
	}
}

// chatMessage represents a single message in an OpenAI chat request.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// inferenceRequest is a minimal struct to extract the model, stream flag,
// and messages from OpenAI-compatible request bodies.
type inferenceRequest struct {
	Model    string        `json:"model"`
	Stream   bool          `json:"stream"`
	Messages []chatMessage `json:"messages"`
	Prompt   string        `json:"prompt"`
}

// lastUserPrompt extracts the last user message for semantic classification.
func (r *inferenceRequest) lastUserPrompt() string {
	if r.Prompt != "" {
		return r.Prompt
	}
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].Content
		}
	}
	return ""
}

// ServeHTTP handles /v1/chat/completions and /v1/completions.
// If the request body contains "stream": true, SSE-compatible response
// headers are set before proxying so that clients receive chunked tokens.
func (p *InferenceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Read the body so we can peek at the model field.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Parse the model and stream flag from the request.
	var req inferenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeProxyError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Model == "" {
		writeProxyError(w, http.StatusBadRequest, "model field is required")
		return
	}

	// Semantic routing: if model is "auto", classify the prompt and pick the tier.
	if req.Model == "auto" && p.SemanticRouter != nil {
		prompt := req.lastUserPrompt()
		if prompt != "" {
			mappedModel, tier, confidence, classifyErr := p.SemanticRouter.Classify(prompt)
			if classifyErr != nil {
				log.Printf("semantic routing failed, falling back to default: %v", classifyErr)
			} else if mappedModel != "" {
				req.Model = mappedModel
				w.Header().Set("X-Semantic-Tier", tier)
				w.Header().Set("X-Semantic-Confidence", fmt.Sprintf("%.2f", confidence))
				log.Printf("semantic routing: tier=%s model=%s confidence=%.2f", tier, mappedModel, confidence)
				// Rewrite the body with the resolved model name
				body = bytes.Replace(body, []byte(`"auto"`), []byte(fmt.Sprintf(`%q`, mappedModel)), 1)
			}
		}
	}

	// Load shedding: reject if too many in-flight requests for this model
	if p.maxInflight > 0 {
		count := p.incrementInflight(req.Model)
		defer p.decrementInflight(req.Model)
		if count > int64(p.maxInflight) {
			w.Header().Set("Retry-After", "5")
			writeProxyError(w, http.StatusServiceUnavailable,
				fmt.Sprintf("model %s overloaded: %d in-flight requests (max %d)", req.Model, count, p.maxInflight))
			return
		}
	} else {
		// Even without load shedding, track in-flight for metrics
		p.incrementInflight(req.Model)
		defer p.decrementInflight(req.Model)
	}

	// Select a backend.
	backend, reason, err := p.SelectBackend(req.Model, r.Header)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Restore the body for the proxy request.
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Inject routing reason header in response.
	w.Header().Set("X-Fleet-Routing-Reason", reason)

	// If the client requested streaming, prepare SSE response headers.
	// The actual streaming is handled in ProxyRequest when it detects
	// the backend's Content-Type: text/event-stream response.
	if req.Stream {
		w.Header().Set("X-Fleet-Stream-Requested", "true")
	}

	p.ProxyRequest(w, r, backend)
}
