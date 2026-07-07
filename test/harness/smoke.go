package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RunSmoke performs basic health probes, authentication checks, a CRUD lifecycle,
// metrics verification, and reachability of all known endpoints.
func RunSmoke(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "smoke", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	// --- Health probes ---
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, lat, err := doRequest("GET", cfg.BaseURL+path, nil, nil)
		if err != nil {
			check(&sr, "health:"+path, false, fmt.Sprintf("request error: %v", err), 0)
			continue
		}
		body := readBody(resp)
		ok := statusOK(resp.StatusCode)
		check(&sr, "health:"+path, ok,
			fmt.Sprintf("status=%s body=%s", statusText(resp), truncate(body, 80)),
			lat.Milliseconds())
	}

	// --- Auth check: unauthenticated request to protected endpoint ---
	{
		resp, _, err := doRequest("GET", cfg.BaseURL+"/api/v1/clusters", nil, nil)
		if err != nil {
			check(&sr, "auth:no-token-rejected", false, fmt.Sprintf("request error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "auth:no-token-rejected", resp.StatusCode == 401,
				fmt.Sprintf("expected 401, got %s", statusText(resp)), 0)
		}
	}

	// --- Auth check: valid token accepted ---
	if cfg.Token != "" {
		resp, _, err := authGet(cfg.BaseURL, "/api/v1/clusters", cfg.Token)
		if err != nil {
			check(&sr, "auth:valid-token-accepted", false, fmt.Sprintf("request error: %v", err), 0)
		} else {
			drainBody(resp)
			check(&sr, "auth:valid-token-accepted", statusOK(resp.StatusCode),
				fmt.Sprintf("status=%s", statusText(resp)), 0)
		}
	} else {
		skip(&sr, "auth:valid-token-accepted", "no token provided")
	}

	// --- CRUD lifecycle: create, read, delete ---
	if cfg.Token != "" {
		clusterName := uniqueID("smoke")
		payload := clusterPayload(clusterName)

		// Create
		resp, lat, err := authPost(cfg.BaseURL, "/api/v1/clusters", cfg.Token,
			strings.NewReader(payload))
		if err != nil {
			check(&sr, "crud:create-cluster", false, fmt.Sprintf("request error: %v", err), 0)
		} else {
			body := readBody(resp)
			created := statusOK(resp.StatusCode) || resp.StatusCode == 201
			check(&sr, "crud:create-cluster", created,
				fmt.Sprintf("status=%s body=%s", statusText(resp), truncate(body, 120)),
				lat.Milliseconds())

			// Extract ID from response for delete (best-effort).
			clusterID := extractID(body, clusterName)

			// Read
			resp2, lat2, err2 := authGet(cfg.BaseURL, "/api/v1/clusters", cfg.Token)
			if err2 != nil {
				check(&sr, "crud:read-clusters", false, fmt.Sprintf("request error: %v", err2), 0)
			} else {
				body2 := readBody(resp2)
				check(&sr, "crud:read-clusters", statusOK(resp2.StatusCode),
					fmt.Sprintf("status=%s contains=%v", statusText(resp2),
						strings.Contains(body2, clusterName)),
					lat2.Milliseconds())
			}

			// Delete
			if clusterID != "" {
				resp3, lat3, err3 := authDelete(cfg.BaseURL, "/api/v1/clusters/"+clusterID, cfg.Token)
				if err3 != nil {
					check(&sr, "crud:delete-cluster", false, fmt.Sprintf("request error: %v", err3), 0)
				} else {
					drainBody(resp3)
					check(&sr, "crud:delete-cluster",
						statusOK(resp3.StatusCode) || resp3.StatusCode == 204,
						fmt.Sprintf("status=%s", statusText(resp3)),
						lat3.Milliseconds())
				}
			} else {
				check(&sr, "crud:delete-cluster", false, "could not extract cluster ID from create response", 0)
			}
		}
	} else {
		skip(&sr, "crud:create-cluster", "no token provided")
		skip(&sr, "crud:read-clusters", "no token provided")
		skip(&sr, "crud:delete-cluster", "no token provided")
	}

	// --- Metrics check ---
	if cfg.MetricsURL != "" {
		resp, lat, err := doRequest("GET", cfg.MetricsURL+"/metrics", nil, nil)
		if err != nil {
			check(&sr, "metrics:reachable", false, fmt.Sprintf("request error: %v", err), 0)
		} else {
			body := readBody(resp)
			check(&sr, "metrics:reachable", statusOK(resp.StatusCode),
				fmt.Sprintf("status=%s body_len=%d", statusText(resp), len(body)),
				lat.Milliseconds())
		}
	} else {
		skip(&sr, "metrics:reachable", "no metrics URL provided")
	}

	// --- Endpoint reachability: all 15+ endpoints ---
	endpoints := allEndpoints()
	for _, ep := range endpoints {
		name := fmt.Sprintf("reach:%s:%s", ep.Method, ep.Path)
		var resp *http.Response
		var lat time.Duration
		var err error

		if !ep.Auth {
			resp, lat, err = doRequest(ep.Method, cfg.BaseURL+ep.Path, nil, nil)
		} else if cfg.Token != "" {
			if ep.Method == "POST" {
				body := bodyForEndpoint(ep.Path)
				resp, lat, err = authPost(cfg.BaseURL, ep.Path, cfg.Token,
					strings.NewReader(body))
			} else if ep.Method == "DELETE" {
				resp, lat, err = authDelete(cfg.BaseURL, ep.Path, cfg.Token)
			} else {
				resp, lat, err = authGet(cfg.BaseURL, ep.Path, cfg.Token)
			}
		} else {
			skip(&sr, name, "no token provided")
			continue
		}

		if err != nil {
			check(&sr, name, false, fmt.Sprintf("request error: %v", err), 0)
		} else {
			drainBody(resp)
			// Any response (even 404/405) means the endpoint is reachable.
			// We fail only on connection errors.
			reachable := resp.StatusCode > 0
			check(&sr, name, reachable,
				fmt.Sprintf("status=%s latency=%s", statusText(resp), formatDuration(lat)),
				lat.Milliseconds())
		}
	}

	return sr
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// extractID tries to extract an ID from a JSON response body.
// It does simple string scanning to avoid importing encoding/json here
// (it is already imported in config.go).
func extractID(body, name string) string {
	// Try "id":"..." pattern.
	for _, key := range []string{`"id":"`, `"id": "`} {
		idx := strings.Index(body, key)
		if idx >= 0 {
			rest := body[idx+len(key):]
			end := strings.Index(rest, `"`)
			if end > 0 {
				return rest[:end]
			}
		}
	}
	// Fall back to using the name as the ID.
	return name
}
