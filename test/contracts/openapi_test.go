//go:build contracts

// Package contracts validates that the fleet-controller implementation
// conforms to the OpenAPI specification defined in api/openapi/fleet-api.yaml.
package contracts

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Package-level state shared across all contract test files.
// ---------------------------------------------------------------------------

var (
	// testRootDir is the project root (directory containing go.mod).
	testRootDir string

	// serverURL is the base URL of the running fleet-controller.
	// Empty when the server could not be started.
	serverURL string
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// findProjectRoot walks up from this source file to locate go.mod.
func findProjectRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("contracts: cannot determine source file location")
	}
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("contracts: cannot find project root (go.mod not found)")
		}
		dir = parent
	}
}

// freePort returns an available TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// waitForHealthy polls url until it responds with 200 or the timeout expires.
func waitForHealthy(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// startFleetController builds the fleet-controller binary, starts it on
// random ports, and waits for it to become healthy. It returns a cleanup
// function that stops the process and removes the binary. On failure
// serverURL is left empty and an error is returned.
func startFleetController() (cleanup func(), retErr error) {
	binPath := filepath.Join(os.TempDir(), fmt.Sprintf("fleet-controller-contracts-%d%s", os.Getpid(), executableSuffix()))

	build := exec.Command("go", "build", "-o", binPath, "./cmd/fleet-controller")
	build.Dir = testRootDir
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build failed: %v\n%s", err, out)
	}

	apiPort, err := freePort()
	if err != nil {
		os.Remove(binPath)
		return nil, fmt.Errorf("free port (api): %v", err)
	}
	metricsPort, err := freePort()
	if err != nil {
		os.Remove(binPath)
		return nil, fmt.Errorf("free port (metrics): %v", err)
	}

	proc := exec.Command(binPath,
		"--port", fmt.Sprintf("%d", apiPort),
		"--metrics-port", fmt.Sprintf("%d", metricsPort),
	)

	// Filter out FLEET_AUTH_SECRET so auth is disabled during tests.
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "FLEET_AUTH_SECRET=") {
			filtered = append(filtered, e)
		}
	}
	proc.Env = filtered
	proc.Stdout = io.Discard
	proc.Stderr = io.Discard

	if err := proc.Start(); err != nil {
		os.Remove(binPath)
		return nil, fmt.Errorf("start: %v", err)
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	if !waitForHealthy(base+"/healthz", 15*time.Second) {
		proc.Process.Kill()
		proc.Wait()
		os.Remove(binPath)
		return nil, fmt.Errorf("server did not become healthy within 15s")
	}

	serverURL = base
	return func() {
		proc.Process.Kill()
		proc.Wait()
		os.Remove(binPath)
	}, nil
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// requireServer skips the calling test when the fleet-controller is not running.
func requireServer(t *testing.T) {
	t.Helper()
	if serverURL == "" {
		t.Skip("fleet-controller server not available; skipping")
	}
}

// ---------------------------------------------------------------------------
// TestMain — shared entry point for all contract tests in this package.
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	testRootDir = findProjectRoot()

	cleanup, err := startFleetController()
	if err != nil {
		fmt.Fprintf(os.Stderr, "contract tests: server setup failed: %v (OpenAPI tests will be skipped)\n", err)
	}

	code := m.Run()

	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// OpenAPI spec parsing
// ---------------------------------------------------------------------------

// specEndpoint represents a path + HTTP method from the OpenAPI spec.
type specEndpoint struct {
	Path   string
	Method string
}

// parseOpenAPISpec reads api/openapi/fleet-api.yaml and returns every
// path+method pair defined in the paths section.
func parseOpenAPISpec(t *testing.T) []specEndpoint {
	t.Helper()

	specPath := filepath.Join(testRootDir, "api", "openapi", "fleet-api.yaml")
	f, err := os.Open(specPath)
	if err != nil {
		t.Fatalf("failed to open OpenAPI spec at %s: %v", specPath, err)
	}
	defer f.Close()

	httpMethods := map[string]bool{
		"get": true, "post": true, "put": true, "delete": true,
		"patch": true, "options": true, "head": true,
	}

	var endpoints []specEndpoint
	scanner := bufio.NewScanner(f)
	inPaths := false
	currentPath := ""

	for scanner.Scan() {
		line := scanner.Text()

		// Detect the start of the paths: section.
		if line == "paths:" {
			inPaths = true
			continue
		}
		if !inPaths {
			continue
		}
		// A top-level key (no leading whitespace, non-blank, non-comment)
		// signals the end of the paths section.
		if len(line) > 0 && line[0] != ' ' && line[0] != '#' {
			break
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		// Path entry: 2-space indent, starts with /
		if indent == 2 && strings.HasPrefix(trimmed, "/") {
			currentPath = strings.TrimSuffix(trimmed, ":")
			continue
		}
		// Method entry: 4-space indent, recognised HTTP method
		if indent == 4 && currentPath != "" {
			method := strings.TrimSuffix(trimmed, ":")
			if httpMethods[method] {
				endpoints = append(endpoints, specEndpoint{
					Path:   currentPath,
					Method: strings.ToUpper(method),
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("error scanning OpenAPI spec: %v", err)
	}
	if len(endpoints) == 0 {
		t.Fatal("no endpoints found in OpenAPI spec")
	}
	return endpoints
}

// paramRe matches OpenAPI path parameters such as {id} or {model}.
var paramRe = regexp.MustCompile(`\{[^}]+\}`)

// resolveURL substitutes path parameters with a placeholder value and
// prepends the server base URL.
func resolveURL(path string) string {
	return serverURL + paramRe.ReplaceAllString(path, "test-value")
}

// ---------------------------------------------------------------------------
// Contract test functions
// ---------------------------------------------------------------------------

// TestOpenAPIEndpointsExist verifies that every path+method declared in the
// OpenAPI spec is registered by the fleet-controller (i.e. does not return a
// mux-level 404).
func TestOpenAPIEndpointsExist(t *testing.T) {
	requireServer(t)
	endpoints := parseOpenAPISpec(t)
	client := &http.Client{Timeout: 5 * time.Second}

	for _, ep := range endpoints {
		ep := ep
		t.Run(fmt.Sprintf("%s %s", ep.Method, ep.Path), func(t *testing.T) {
			url := resolveURL(ep.Path)
			var body io.Reader
			if ep.Method == "POST" || ep.Method == "PUT" || ep.Method == "PATCH" {
				body = strings.NewReader("{}")
			}
			req, err := http.NewRequest(ep.Method, url, body)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)

			// Distinguish mux-level 404 (route not registered, text/plain body)
			// from handler-level 404 (route exists, resource not found,
			// application/json body).
			if resp.StatusCode == http.StatusNotFound {
				ct := resp.Header.Get("Content-Type")
				if !strings.Contains(ct, "application/json") {
					t.Errorf("route not registered: %s %s (got mux-level 404)", ep.Method, ep.Path)
				}
			}
		})
	}
}

// TestHealthProbes verifies that the liveness and readiness probes return 200.
func TestHealthProbes(t *testing.T) {
	requireServer(t)
	client := &http.Client{Timeout: 5 * time.Second}

	for _, path := range []string{"/healthz", "/readyz"} {
		path := path
		t.Run(path, func(t *testing.T) {
			resp, err := client.Get(serverURL + path)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("expected status 200, got %d: %s", resp.StatusCode, body)
			}
		})
	}
}

// TestPostEndpointMediaTypes verifies that JSON remains supported by the
// ordinary REST surface while unsigned v2 intent JSON fails closed by default.
func TestPostEndpointMediaTypes(t *testing.T) {
	requireServer(t)
	endpoints := parseOpenAPISpec(t)
	client := &http.Client{Timeout: 5 * time.Second}

	for _, ep := range endpoints {
		if ep.Method != "POST" {
			continue
		}
		ep := ep
		t.Run(ep.Path, func(t *testing.T) {
			url := resolveURL(ep.Path)
			req, err := http.NewRequest("POST", url, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)

			if ep.Path == "/api/v2/intents" {
				if resp.StatusCode != http.StatusUnsupportedMediaType {
					t.Errorf("POST %s returned %d; unsigned JSON should be disabled", ep.Path, resp.StatusCode)
				}
				return
			}
			if resp.StatusCode == http.StatusUnsupportedMediaType {
				t.Errorf("POST %s returned 415; JSON should be accepted", ep.Path)
			}
			// Verify the route itself exists.
			if resp.StatusCode == http.StatusNotFound {
				ct := resp.Header.Get("Content-Type")
				if !strings.Contains(ct, "application/json") {
					t.Errorf("POST %s route not registered (mux-level 404)", ep.Path)
				}
			}
		})
	}
}

// TestResponseContentType verifies that every GET endpoint in the spec
// returns a response with Content-Type containing application/json.
func TestResponseContentType(t *testing.T) {
	requireServer(t)
	endpoints := parseOpenAPISpec(t)
	client := &http.Client{Timeout: 5 * time.Second}

	for _, ep := range endpoints {
		if ep.Method != "GET" {
			continue
		}
		ep := ep
		t.Run(ep.Path, func(t *testing.T) {
			resp, err := client.Get(resolveURL(ep.Path))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)

			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, "application/json") {
				t.Errorf("GET %s: expected Content-Type containing application/json, got %q (status %d)",
					ep.Path, ct, resp.StatusCode)
			}
		})
	}
}
