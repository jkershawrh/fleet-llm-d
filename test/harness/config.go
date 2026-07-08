package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Config holds the connection parameters for the fleet-controller under test.
type Config struct {
	BaseURL        string // e.g. http://localhost:8080
	MetricsURL     string // e.g. http://localhost:9090
	Token          string // Bearer token for authenticated endpoints
	Secret         string // HMAC secret for generating tokens (redteam)
	Duration       time.Duration
	Output         string // path for JSON report
	InferenceModel  string // Model name for inference tests (skips auto-discovery)
	InferenceModels string // Comma-separated model list for multi-model tests
}

// SuiteResult captures the outcome of one test suite.
type SuiteResult struct {
	Name      string        `json:"name"`
	Passed    int           `json:"passed"`
	Failed    int           `json:"failed"`
	Skipped   int           `json:"skipped"`
	Duration  time.Duration `json:"duration_ns"`
	Checks    []CheckResult `json:"checks"`
	Latencies *LatencyStats `json:"latencies,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

// CheckResult records a single assertion within a suite.
type CheckResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

// LatencyStats holds computed percentiles and summary stats.
type LatencyStats struct {
	P50  float64 `json:"p50_ms"`
	P95  float64 `json:"p95_ms"`
	P99  float64 `json:"p99_ms"`
	Min  float64 `json:"min_ms"`
	Max  float64 `json:"max_ms"`
	Mean float64 `json:"mean_ms"`
}

// Claims mirrors the token claims structure without importing pkg/auth.
type Claims struct {
	Subject   string `json:"sub"`
	Role      string `json:"role"`
	IssuedAt  string `json:"iat"`
	ExpiresAt string `json:"exp"`
}

// generateToken creates an HMAC-SHA256 signed token from the given claims.
// Reimplements the logic from pkg/auth/token.go without importing it.
func generateToken(secret string, sub, role string, duration time.Duration) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("secret must not be empty")
	}
	now := time.Now().UTC()
	claims := Claims{
		Subject:   sub,
		Role:      role,
		IssuedAt:  now.Format(time.RFC3339),
		ExpiresAt: now.Add(duration).Format(time.RFC3339),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(claimsJSON)
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return claimsB64 + "." + sigB64, nil
}

// generateExpiredToken creates a token that has already expired.
func generateExpiredToken(secret string) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		Subject:   "expired-user",
		Role:      "admin",
		IssuedAt:  now.Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(-1 * time.Hour).Format(time.RFC3339),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(claimsJSON)
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return claimsB64 + "." + sigB64, nil
}

// generateTamperedToken creates a token where the claims have been altered
// after signing (signature mismatch).
func generateTamperedToken(secret string) (string, error) {
	now := time.Now().UTC()
	origClaims := Claims{
		Subject:   "original-user",
		Role:      "viewer",
		IssuedAt:  now.Format(time.RFC3339),
		ExpiresAt: now.Add(1 * time.Hour).Format(time.RFC3339),
	}
	origJSON, err := json.Marshal(origClaims)
	if err != nil {
		return "", fmt.Errorf("marshal original claims: %w", err)
	}
	// Sign original claims.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(origJSON)
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	// Tamper the claims: change role to admin.
	tampered := Claims{
		Subject:   "original-user",
		Role:      "admin",
		IssuedAt:  now.Format(time.RFC3339),
		ExpiresAt: now.Add(1 * time.Hour).Format(time.RFC3339),
	}
	tamperedJSON, err := json.Marshal(tampered)
	if err != nil {
		return "", fmt.Errorf("marshal tampered claims: %w", err)
	}
	tamperedB64 := base64.RawURLEncoding.EncodeToString(tamperedJSON)
	return tamperedB64 + "." + sigB64, nil
}

// doRequest performs an HTTP request and returns the response.
// The caller is responsible for closing the response body.
func doRequest(method, url string, body io.Reader, headers map[string]string) (*http.Response, time.Duration, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return nil, elapsed, fmt.Errorf("do request: %w", err)
	}
	return resp, elapsed, nil
}

// authGet performs an authenticated GET request.
func authGet(baseURL, path, token string) (*http.Response, time.Duration, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	return doRequest(http.MethodGet, baseURL+path, nil, headers)
}

// authPost performs an authenticated POST request with a JSON body.
func authPost(baseURL, path, token string, body io.Reader) (*http.Response, time.Duration, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"Content-Type":  "application/json",
	}
	return doRequest(http.MethodPost, baseURL+path, body, headers)
}

// authDelete performs an authenticated DELETE request.
func authDelete(baseURL, path, token string) (*http.Response, time.Duration, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	return doRequest(http.MethodDelete, baseURL+path, nil, headers)
}

// readBody reads and closes the response body, returning the content as a string.
func readBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return fmt.Sprintf("<read error: %v>", err)
	}
	return string(data)
}

// percentile computes the pth percentile from a sorted slice of float64.
// p should be in [0, 100].
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := (p / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}
	frac := rank - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

// computeLatencyStats calculates percentiles and summary stats from raw durations.
func computeLatencyStats(durations []time.Duration) *LatencyStats {
	if len(durations) == 0 {
		return &LatencyStats{}
	}
	ms := make([]float64, len(durations))
	sum := 0.0
	for i, d := range durations {
		ms[i] = float64(d.Microseconds()) / 1000.0
		sum += ms[i]
	}
	sort.Float64s(ms)
	return &LatencyStats{
		P50:  percentile(ms, 50),
		P95:  percentile(ms, 95),
		P99:  percentile(ms, 99),
		Min:  ms[0],
		Max:  ms[len(ms)-1],
		Mean: sum / float64(len(ms)),
	}
}

// check is a helper that appends a CheckResult to the suite.
func check(sr *SuiteResult, name string, passed bool, detail string, latencyMs int64) {
	sr.Checks = append(sr.Checks, CheckResult{
		Name:    name,
		Passed:  passed,
		Detail:  detail,
		Latency: latencyMs,
	})
	if passed {
		sr.Passed++
	} else {
		sr.Failed++
	}
}

// skip is a helper that appends a skipped CheckResult to the suite.
func skip(sr *SuiteResult, name, reason string) {
	sr.Checks = append(sr.Checks, CheckResult{
		Name:   name,
		Passed: false,
		Detail: "SKIP: " + reason,
	})
	sr.Skipped++
}

// drainBody reads and discards the response body to allow connection reuse.
func drainBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// allEndpoints returns all known API endpoint paths.
func allEndpoints() []struct {
	Method string
	Path   string
	Auth   bool
} {
	return []struct {
		Method string
		Path   string
		Auth   bool
	}{
		{"GET", "/healthz", false},
		{"GET", "/readyz", false},
		{"GET", "/api/v1/clusters", true},
		{"POST", "/api/v1/clusters", true},
		{"GET", "/api/v1/pools", true},
		{"GET", "/api/v1/tenants", true},
		{"GET", "/api/v1/metrics/fleet", true},
		{"GET", "/api/v1/rollouts", true},
		{"POST", "/api/v1/rollouts", true},
		{"GET", "/api/v1/verify/chains", true},
		{"POST", "/v1/chat/completions", true},
		{"POST", "/v1/completions", true},
		{"POST", "/api/v1/webhook/fleetinferencepool", true},
		{"POST", "/api/v1/webhook/validate", true},
		{"GET", "/api/v1/tenants/test-tenant/usage", true},
		{"GET", "/api/v1/metrics/model/test-model", true},
	}
}

// clusterPayload returns a JSON body for creating a cluster with the given name.
func clusterPayload(name string) string {
	return fmt.Sprintf(`{"name":%q,"provider":"aws","region":"us-east-1"}`, name)
}

// rolloutPayload returns a JSON body for creating a rollout.
func rolloutPayload() string {
	return `{"model":"test-model","target_version":"v2","strategy":"canary"}`
}

// chatPayload returns a JSON body for a chat completions request.
func chatPayload() string {
	return `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
}

// completionPayload returns a JSON body for a completions request.
func completionPayload() string {
	return `{"model":"test-model","prompt":"hello"}`
}

// webhookPayload returns a JSON body for a webhook call.
func webhookPayload() string {
	return `{"kind":"FleetInferencePool","metadata":{"name":"test"}}`
}

// validatePayload returns a JSON body for a validation webhook call.
func validatePayload() string {
	return `{"kind":"Validation","metadata":{"name":"test"}}`
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fus", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000.0)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// statusOK checks whether the status code is in the 2xx range.
func statusOK(code int) bool {
	return code >= 200 && code < 300
}

// statusText returns a compact string like "200 OK" for the given response.
func statusText(resp *http.Response) string {
	if resp == nil {
		return "nil"
	}
	return fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
}

// uniqueID generates a simple unique-ish ID for test data.
func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%100000)
}

// bodyForEndpoint returns an appropriate request body for a POST endpoint.
func bodyForEndpoint(path string) string {
	switch {
	case strings.HasSuffix(path, "/clusters"):
		return clusterPayload(uniqueID("harness"))
	case strings.HasSuffix(path, "/rollouts"):
		return rolloutPayload()
	case strings.HasSuffix(path, "/chat/completions"):
		return chatPayload()
	case strings.HasSuffix(path, "/completions"):
		return completionPayload()
	case strings.HasSuffix(path, "/fleetinferencepool"):
		return webhookPayload()
	case strings.HasSuffix(path, "/validate"):
		return validatePayload()
	default:
		return `{}`
	}
}
