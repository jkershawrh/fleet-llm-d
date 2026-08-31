//go:build contracts

package contracts

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestOpenAPIDrift ensures every route registered in cmd/fleet-controller/main.go
// has a corresponding path in api/openapi/fleet-api.yaml and vice versa.
// Routes intentionally excluded from the spec are listed in the allowlist.
func TestOpenAPIDrift(t *testing.T) {
	root := findProjectRoot()

	codeRoutes := extractCodeRoutes(t, filepath.Join(root, "pkg", "server", "routes.go"))
	specPaths := extractSpecPaths(t, filepath.Join(root, "api", "openapi", "fleet-api.yaml"))

	// Routes that are intentionally not in the OpenAPI spec.
	allowlist := map[string]string{
		"GET /metrics":    "served on metrics port (9091), not the API port",
		"GET /debug/vars": "served on metrics port (9091), not the API port",
	}

	// Check: every code route must be in the spec or the allowlist.
	var missingFromSpec []string
	for _, route := range codeRoutes {
		if _, ok := allowlist[route]; ok {
			continue
		}
		method, path := splitRoute(route)
		if !specHasPath(specPaths, method, path) {
			missingFromSpec = append(missingFromSpec, route)
		}
	}
	if len(missingFromSpec) > 0 {
		t.Errorf("routes registered in code but missing from OpenAPI spec:\n  %s",
			strings.Join(missingFromSpec, "\n  "))
	}

	// Check: every spec path must have a corresponding code route.
	var missingFromCode []string
	for _, sp := range specPaths {
		found := false
		for _, route := range codeRoutes {
			method, path := splitRoute(route)
			if method == sp.method && normalizePath(path) == normalizePath(sp.path) {
				found = true
				break
			}
		}
		if !found {
			missingFromCode = append(missingFromCode, sp.method+" "+sp.path)
		}
	}
	if len(missingFromCode) > 0 {
		t.Errorf("paths in OpenAPI spec but not registered in code:\n  %s",
			strings.Join(missingFromCode, "\n  "))
	}

	t.Logf("code routes: %d, spec paths: %d, allowlisted: %d",
		len(codeRoutes), len(specPaths), len(allowlist))
}

type specPath struct {
	method string
	path   string
}

// extractCodeRoutes parses mux.HandleFunc/mux.Handle calls from main.go.
func extractCodeRoutes(t *testing.T, mainPath string) []string {
	t.Helper()
	f, err := os.Open(mainPath)
	if err != nil {
		t.Fatalf("open main.go: %v", err)
	}
	defer f.Close()

	re := regexp.MustCompile(`mux\.Handle(?:Func)?\("(GET|POST|PUT|PATCH|DELETE)\s+([^"]+)"`)
	var routes []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		matches := re.FindStringSubmatch(scanner.Text())
		if matches != nil {
			routes = append(routes, matches[1]+" "+matches[2])
		}
	}
	sort.Strings(routes)
	return routes
}

// extractSpecPaths parses path entries from the OpenAPI YAML.
func extractSpecPaths(t *testing.T, specFile string) []specPath {
	t.Helper()
	f, err := os.Open(specFile)
	if err != nil {
		t.Fatalf("open spec: %v", err)
	}
	defer f.Close()

	pathRe := regexp.MustCompile(`^  (/[^:]+):`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):`)

	var paths []specPath
	var currentPath string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if m := pathRe.FindStringSubmatch(line); m != nil {
			currentPath = strings.TrimSpace(m[1])
		} else if m := methodRe.FindStringSubmatch(line); m != nil && currentPath != "" {
			paths = append(paths, specPath{
				method: strings.ToUpper(m[1]),
				path:   currentPath,
			})
		}
	}
	return paths
}

func splitRoute(route string) (method, path string) {
	parts := strings.SplitN(route, " ", 2)
	if len(parts) != 2 {
		return "", route
	}
	return parts[0], parts[1]
}

// normalizePath converts {param} style to a canonical form for comparison.
func normalizePath(p string) string {
	re := regexp.MustCompile(`\{[^}]+\}`)
	return re.ReplaceAllString(p, "{}")
}

func specHasPath(paths []specPath, method, codePath string) bool {
	normalized := normalizePath(codePath)
	for _, sp := range paths {
		if sp.method == method && normalizePath(sp.path) == normalized {
			return true
		}
	}
	return false
}
