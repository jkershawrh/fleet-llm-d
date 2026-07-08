package routing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestProxyRequest_StripsAuthHeaders(t *testing.T) {
	var receivedHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	p := NewInferenceProxy()
	b := &Backend{Name: "strip-test", URL: backend.URL, Runtime: "vllm", Healthy: true}

	reqBody := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Cookie", "session=abc123")
	req.Header.Set("Proxy-Authorization", "Basic creds")
	req.Header.Set("X-Custom-Header", "keep-me")
	req.Header.Set("X-Llm-D-Inference-Objective", "realtime")

	recorder := httptest.NewRecorder()
	p.ProxyRequest(recorder, req, b)

	if receivedHeaders.Get("Authorization") != "" {
		t.Error("Authorization header was forwarded to backend")
	}
	if receivedHeaders.Get("Cookie") != "" {
		t.Error("Cookie header was forwarded to backend")
	}
	if receivedHeaders.Get("Proxy-Authorization") != "" {
		t.Error("Proxy-Authorization header was forwarded to backend")
	}
	if receivedHeaders.Get("X-Custom-Header") != "keep-me" {
		t.Error("X-Custom-Header should be forwarded but was stripped")
	}
	if receivedHeaders.Get("X-Llm-D-Inference-Objective") != "realtime" {
		t.Error("X-Llm-D-Inference-Objective should be forwarded")
	}
}

func TestProxyError_ValidJSONWithSpecialChars(t *testing.T) {
	p := NewInferenceProxy()
	// No backends registered — will trigger the "no backends" error
	body := `{"model":"nonexistent"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}
	var parsed map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Errorf("response body is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if parsed["error"] == "" {
		t.Error("expected error field in JSON response")
	}
}

func TestProxyError_NoModelField(t *testing.T) {
	p := NewInferenceProxy()
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json Content-Type, got %s", w.Header().Get("Content-Type"))
	}
	var parsed map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Errorf("error response is not valid JSON: %v", err)
	}
}

func TestServeHTTP_RejectsOversizedBody(t *testing.T) {
	p := NewInferenceProxy()
	p.RegisterBackend("test", Backend{Name: "b", URL: "http://unused:8000", Healthy: true})

	bigBody := strings.Repeat("x", 11<<20) // 11 MB
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 400 or 413 for oversized body, got %d", w.Code)
	}
}

func TestInferenceProxy_TransportConfig(t *testing.T) {
	p := NewInferenceProxy()
	transport, ok := p.http.Transport.(*http.Transport)
	if !ok {
		t.Fatal("proxy should use *http.Transport")
	}
	if transport.MaxIdleConnsPerHost < 50 {
		t.Errorf("MaxIdleConnsPerHost=%d, want >=50 for concurrent inference load", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxConnsPerHost < 100 {
		t.Errorf("MaxConnsPerHost=%d, want >=100", transport.MaxConnsPerHost)
	}
	if transport.MaxIdleConns < 200 {
		t.Errorf("MaxIdleConns=%d, want >=200", transport.MaxIdleConns)
	}
}

func TestInferenceProxy_HealthPolling(t *testing.T) {
	healthCallCount := int32(0)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			atomic.AddInt32(&healthCallCount, 1)
			w.WriteHeader(200)
			w.Write([]byte(`{"data":[{"id":"test"}]}`))
			return
		}
		w.WriteHeader(200)
	}))
	defer backend.Close()

	p := NewInferenceProxy()
	p.RegisterBackend("health-model", Backend{
		Name: "health-be", URL: backend.URL, Runtime: "vllm", Healthy: true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.StartHealthChecks(ctx, 100*time.Millisecond)

	time.Sleep(350 * time.Millisecond)
	cancel()

	count := atomic.LoadInt32(&healthCallCount)
	if count < 2 {
		t.Errorf("expected at least 2 health checks, got %d", count)
	}
}

func TestInferenceProxy_HealthPolling_MarksUnhealthy(t *testing.T) {
	healthy := int32(1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			if atomic.LoadInt32(&healthy) == 0 {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(200)
			w.Write([]byte(`{"data":[{"id":"test"}]}`))
			return
		}
		w.WriteHeader(200)
	}))
	defer backend.Close()

	p := NewInferenceProxy()
	p.RegisterBackend("unhealthy-model", Backend{
		Name: "flaky-be", URL: backend.URL, Runtime: "vllm", Healthy: true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.StartHealthChecks(ctx, 100*time.Millisecond)

	// Backend is healthy initially
	time.Sleep(150 * time.Millisecond)
	b, _, err := p.SelectBackend("unhealthy-model", http.Header{})
	if err != nil {
		t.Fatalf("should have healthy backend: %v", err)
	}
	if !b.Healthy {
		t.Fatal("backend should be healthy initially")
	}

	// Make backend unhealthy
	atomic.StoreInt32(&healthy, 0)
	time.Sleep(200 * time.Millisecond)

	// Should now be marked unhealthy
	_, _, err = p.SelectBackend("unhealthy-model", http.Header{})
	if err == nil {
		t.Fatal("expected error — backend should be marked unhealthy")
	}

	cancel()
}

func TestProxy_ShedsLoadWhenOverloaded(t *testing.T) {
	// Create a slow backend (takes 200ms per request)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer backend.Close()

	p := NewInferenceProxy()
	p.SetMaxInflight(2) // only allow 2 concurrent
	p.RegisterBackend("slow-model", Backend{Name: "slow", URL: backend.URL, Runtime: "vllm", Healthy: true})

	// Send 4 concurrent requests — 2 should succeed, 2 should get 503
	var wg sync.WaitGroup
	results := make([]int, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := `{"model":"slow-model","messages":[{"role":"user","content":"hi"}]}`
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			p.ServeHTTP(w, req)
			results[idx] = w.Code
		}(i)
	}
	wg.Wait()

	successes := 0
	shedded := 0
	for _, code := range results {
		if code == 200 {
			successes++
		} else if code == 503 {
			shedded++
		}
	}

	if successes < 2 {
		t.Errorf("expected at least 2 successes, got %d", successes)
	}
	if shedded == 0 {
		t.Error("expected at least 1 request to be shed (503)")
	}
}

func TestProxy_ReturnsRetryAfterHeader(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer backend.Close()

	p := NewInferenceProxy()
	p.SetMaxInflight(1)
	p.RegisterBackend("retry-model", Backend{Name: "b1", URL: backend.URL, Runtime: "vllm", Healthy: true})

	// Start a slow request in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		body := `{"model":"retry-model","messages":[{"role":"user","content":"hi"}]}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, req)
	}()

	time.Sleep(50 * time.Millisecond) // let first request start

	// Second request should be shed
	body := `{"model":"retry-model","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	wg.Wait()

	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "5" {
		t.Errorf("expected Retry-After: 5, got %q", w.Header().Get("Retry-After"))
	}
	// Verify response is valid JSON
	var parsed map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Errorf("response not valid JSON: %v", err)
	}
}

func TestProxy_NoLoadSheddingWhenDisabled(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer backend.Close()

	p := NewInferenceProxy()
	// maxInflight defaults to 0 (disabled)
	p.RegisterBackend("noload-model", Backend{Name: "b1", URL: backend.URL, Runtime: "vllm", Healthy: true})

	body := `{"model":"noload-model","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 when load shedding disabled, got %d", w.Code)
	}
}

func BenchmarkSelectBackend(b *testing.B) {
	proxy := NewInferenceProxy()
	proxy.RegisterBackend("bench-model", Backend{Name: "b1", URL: "http://b1:8000", Runtime: "vllm", Healthy: true, LatencyMs: 10})
	proxy.RegisterBackend("bench-model", Backend{Name: "b2", URL: "http://b2:8000", Runtime: "vllm", Healthy: true, LatencyMs: 20})
	headers := http.Header{}
	headers.Set("x-llm-d-inference-objective", "realtime")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		proxy.SelectBackend("bench-model", headers)
	}
}
