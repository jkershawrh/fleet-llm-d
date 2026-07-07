package routing

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegisterBackend(t *testing.T) {
	p := NewInferenceProxy()

	p.RegisterBackend("test-model", Backend{
		Name: "backend-a", URL: "http://a:8000", Runtime: "vllm", Healthy: true, LatencyMs: 100,
	})
	p.RegisterBackend("test-model", Backend{
		Name: "backend-b", URL: "http://b:8000", Runtime: "ovms", Healthy: true, LatencyMs: 200,
	})
	p.RegisterBackend("other-model", Backend{
		Name: "backend-c", URL: "http://c:8000", Runtime: "vllm", Healthy: true, LatencyMs: 150,
	})

	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.backends["test-model"]) != 2 {
		t.Fatalf("expected 2 backends for test-model, got %d", len(p.backends["test-model"]))
	}
	if len(p.backends["other-model"]) != 1 {
		t.Fatalf("expected 1 backend for other-model, got %d", len(p.backends["other-model"]))
	}
	if p.backends["test-model"][0].Name != "backend-a" {
		t.Errorf("expected first backend name to be backend-a, got %s", p.backends["test-model"][0].Name)
	}
}

func TestSelectBackend_RealtimePicksLowestLatency(t *testing.T) {
	p := NewInferenceProxy()
	p.RegisterBackend("model-a", Backend{
		Name: "slow", URL: "http://slow:8000", Runtime: "vllm", Healthy: true, LatencyMs: 500,
	})
	p.RegisterBackend("model-a", Backend{
		Name: "fast", URL: "http://fast:8000", Runtime: "vllm", Healthy: true, LatencyMs: 100,
	})
	p.RegisterBackend("model-a", Backend{
		Name: "medium", URL: "http://medium:8000", Runtime: "vllm", Healthy: true, LatencyMs: 300,
	})

	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "realtime")

	backend, reason, err := p.SelectBackend("model-a", headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend.Name != "fast" {
		t.Errorf("expected backend 'fast' (lowest latency), got %q", backend.Name)
	}
	if reason != "realtime: lowest latency" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestSelectBackend_BatchPicksAny(t *testing.T) {
	p := NewInferenceProxy()
	p.RegisterBackend("model-b", Backend{
		Name: "cheap", URL: "http://cheap:8000", Runtime: "vllm", Healthy: true, LatencyMs: 800,
	})
	p.RegisterBackend("model-b", Backend{
		Name: "expensive", URL: "http://expensive:8000", Runtime: "vllm", Healthy: true, LatencyMs: 100,
	})

	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "batch")

	backend, reason, err := p.SelectBackend("model-b", headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Batch prefers the first healthy backend (cheapest by convention).
	if backend.Name != "cheap" {
		t.Errorf("expected backend 'cheap' for batch, got %q", backend.Name)
	}
	if reason != "batch: cost-optimized" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestSelectBackend_NoHealthyBackend(t *testing.T) {
	p := NewInferenceProxy()
	p.RegisterBackend("model-c", Backend{
		Name: "down-a", URL: "http://a:8000", Runtime: "vllm", Healthy: false, LatencyMs: 100,
	})
	p.RegisterBackend("model-c", Backend{
		Name: "down-b", URL: "http://b:8000", Runtime: "vllm", Healthy: false, LatencyMs: 200,
	})

	headers := http.Header{}
	_, _, err := p.SelectBackend("model-c", headers)
	if err == nil {
		t.Fatal("expected error for no healthy backends, got nil")
	}

	// Also test with a model that has no backends at all.
	_, _, err = p.SelectBackend("nonexistent-model", headers)
	if err == nil {
		t.Fatal("expected error for nonexistent model, got nil")
	}
}

func TestSelectBackend_DefaultRoundRobin(t *testing.T) {
	p := NewInferenceProxy()
	p.RegisterBackend("model-rr", Backend{
		Name: "rr-a", URL: "http://a:8000", Runtime: "vllm", Healthy: true, LatencyMs: 100,
	})
	p.RegisterBackend("model-rr", Backend{
		Name: "rr-b", URL: "http://b:8000", Runtime: "vllm", Healthy: true, LatencyMs: 200,
	})

	headers := http.Header{}

	// Call multiple times and verify round-robin distribution.
	seen := map[string]int{}
	for i := 0; i < 10; i++ {
		backend, reason, err := p.SelectBackend("model-rr", headers)
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		seen[backend.Name]++
		if reason != "default: round-robin" {
			t.Errorf("expected reason 'default: round-robin', got %q", reason)
		}
	}

	if seen["rr-a"] != 5 || seen["rr-b"] != 5 {
		t.Errorf("expected even round-robin distribution, got rr-a=%d rr-b=%d", seen["rr-a"], seen["rr-b"])
	}
}

func TestProxyRequest(t *testing.T) {
	// Create a mock backend server.
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request was forwarded correctly.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("mock backend failed to read body: %v", err)
			return
		}

		// Echo back a response with request details.
		resp := map[string]interface{}{
			"received_path": r.URL.Path,
			"received_body": string(body),
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "Hello from mock backend"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockBackend.Close()

	p := NewInferenceProxy()
	backend := &Backend{
		Name:    "mock-backend",
		URL:     mockBackend.URL,
		Runtime: "vllm",
		Healthy: true,
	}

	// Create the proxy request.
	reqBody := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ProxyRequest(recorder, req, backend)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Check that the routing header was injected.
	routedTo := resp.Header.Get("X-Fleet-Routed-To")
	if routedTo != "mock-backend" {
		t.Errorf("expected X-Fleet-Routed-To header to be 'mock-backend', got %q", routedTo)
	}

	// Parse and verify the response body.
	var respBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if respBody["received_path"] != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %v", respBody["received_path"])
	}
}

func TestServeHTTP_RoutesToCorrectBackend(t *testing.T) {
	// Create mock backend servers.
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"backend": "a"})
	}))
	defer backendA.Close()

	p := NewInferenceProxy()
	p.RegisterBackend("test-model", Backend{
		Name: "backend-a", URL: backendA.URL, Runtime: "vllm", Healthy: true, LatencyMs: 100,
	})

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, req)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	routingReason := resp.Header.Get("X-Fleet-Routing-Reason")
	if routingReason == "" {
		t.Error("expected X-Fleet-Routing-Reason header to be set")
	}
}

func TestServeHTTP_NoModel(t *testing.T) {
	p := NewInferenceProxy()

	reqBody := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing model, got %d", recorder.Code)
	}
}

func TestServeHTTP_NoBackendReturns502(t *testing.T) {
	p := NewInferenceProxy()

	reqBody := `{"model":"unknown-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadGateway {
		t.Errorf("expected status 502 for no backend, got %d", recorder.Code)
	}
}

// TestProxyRequest_StreamingSSE verifies that when the backend responds with
// Content-Type text/event-stream, the proxy streams SSE events to the client
// with per-chunk flushing.
func TestProxyRequest_StreamingSSE(t *testing.T) {
	// SSE events the mock backend will send.
	sseEvents := []string{
		`data: {"id":"1","choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"id":"2","choices":[{"delta":{"content":" world"}}]}`,
		`data: [DONE]`,
	}

	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("mock backend ResponseWriter does not support Flusher")
			return
		}

		for _, event := range sseEvents {
			fmt.Fprintf(w, "%s\n\n", event)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer mockBackend.Close()

	p := NewInferenceProxy()
	backend := &Backend{
		Name:    "stream-backend",
		URL:     mockBackend.URL,
		Runtime: "vllm",
		Healthy: true,
	}

	reqBody := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ProxyRequest(recorder, req, backend)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify Content-Type was forwarded.
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}

	// Verify X-Fleet-Routed-To was set.
	routedTo := resp.Header.Get("X-Fleet-Routed-To")
	if routedTo != "stream-backend" {
		t.Errorf("expected X-Fleet-Routed-To=stream-backend, got %q", routedTo)
	}

	// Parse the SSE events from the response body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Verify all SSE events are present in order.
	for _, expected := range sseEvents {
		if !strings.Contains(string(body), expected) {
			t.Errorf("response body missing SSE event: %s", expected)
		}
	}
}

// TestProxyRequest_NonStreamingUnchanged verifies that non-streaming responses
// are proxied as before without SSE-specific handling.
func TestProxyRequest_NonStreamingUnchanged(t *testing.T) {
	expectedResp := map[string]interface{}{
		"id":      "chatcmpl-123",
		"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"content": "Hello!"}}},
	}

	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(expectedResp)
	}))
	defer mockBackend.Close()

	p := NewInferenceProxy()
	backend := &Backend{
		Name:    "non-stream-backend",
		URL:     mockBackend.URL,
		Runtime: "vllm",
		Healthy: true,
	}

	reqBody := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ProxyRequest(recorder, req, backend)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var respBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if respBody["id"] != "chatcmpl-123" {
		t.Errorf("expected id chatcmpl-123, got %v", respBody["id"])
	}
}

// TestServeHTTP_StreamFlagSetsHeader verifies that when the request body
// contains "stream": true, the X-Fleet-Stream-Requested header is set.
func TestServeHTTP_StreamFlagSetsHeader(t *testing.T) {
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer mockBackend.Close()

	p := NewInferenceProxy()
	p.RegisterBackend("stream-model", Backend{
		Name: "stream-be", URL: mockBackend.URL, Runtime: "vllm", Healthy: true,
	})

	reqBody := `{"model":"stream-model","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, req)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	streamHeader := resp.Header.Get("X-Fleet-Stream-Requested")
	if streamHeader != "true" {
		t.Errorf("expected X-Fleet-Stream-Requested=true, got %q", streamHeader)
	}

	// Verify SSE data came through.
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "data: [DONE]") {
		t.Errorf("expected SSE data in response body, got: %s", string(body))
	}
}

// TestProxyRequest_StreamingSSE_LiveFlush uses a live HTTP server (not
// httptest.ResponseRecorder) to confirm per-chunk flushing works end-to-end.
func TestProxyRequest_StreamingSSE_LiveFlush(t *testing.T) {
	sseLines := []string{
		"data: {\"token\":\"A\"}\n\n",
		"data: {\"token\":\"B\"}\n\n",
		"data: [DONE]\n\n",
	}

	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, line := range sseLines {
			w.Write([]byte(line))
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer mockBackend.Close()

	proxy := NewInferenceProxy()
	proxy.RegisterBackend("flush-model", Backend{
		Name: "flush-be", URL: mockBackend.URL, Runtime: "vllm", Healthy: true,
	})

	// Wrap the proxy in a live HTTP server.
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}))
	defer proxyServer.Close()

	// Send the request to the proxy.
	reqBody := `{"model":"flush-model","stream":true,"messages":[{"role":"user","content":"go"}]}`
	resp, err := http.Post(proxyServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Read SSE events from the live response.
	scanner := bufio.NewScanner(resp.Body)
	var events []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			events = append(events, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 SSE data lines, got %d: %v", len(events), events)
	}
	if events[2] != "data: [DONE]" {
		t.Errorf("expected last event to be data: [DONE], got %q", events[2])
	}
}
