package main

import (
	"fmt"
	"strings"
	"time"
)

// RunRedTeam exercises security boundaries: invalid tokens, injection attacks,
// path traversals, oversized headers, wrong methods, and other adversarial inputs.
// Token generation is done internally using HMAC-SHA256 (same format as pkg/auth/token.go).
func RunRedTeam(cfg Config) SuiteResult {
	sr := SuiteResult{Name: "redteam", Extra: make(map[string]interface{})}
	start := time.Now()
	defer func() { sr.Duration = time.Since(start) }()

	secret := cfg.Secret
	if secret == "" {
		skip(&sr, "redteam:all", "no --secret provided for token generation")
		return sr
	}

	// --- Test 1: Expired token ---
	{
		token, err := generateExpiredToken(secret)
		if err != nil {
			check(&sr, "redteam:expired-token", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			resp, _, err := authGet(cfg.BaseURL, "/api/v1/clusters", token)
			if err != nil {
				check(&sr, "redteam:expired-token", false, fmt.Sprintf("request error: %v", err), 0)
			} else {
				drainBody(resp)
				check(&sr, "redteam:expired-token", resp.StatusCode == 401,
					fmt.Sprintf("expected 401, got %s", statusText(resp)), 0)
			}
		}
	}

	// --- Test 2: Tampered token (claims modified after signing) ---
	{
		token, err := generateTamperedToken(secret)
		if err != nil {
			check(&sr, "redteam:tampered-token", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			resp, _, err := authGet(cfg.BaseURL, "/api/v1/clusters", token)
			if err != nil {
				check(&sr, "redteam:tampered-token", false, fmt.Sprintf("request error: %v", err), 0)
			} else {
				drainBody(resp)
				check(&sr, "redteam:tampered-token", resp.StatusCode == 401,
					fmt.Sprintf("expected 401, got %s", statusText(resp)), 0)
			}
		}
	}

	// --- Test 3: Token signed with wrong secret ---
	{
		token, err := generateToken("completely-wrong-secret-value", "hacker", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:wrong-secret", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			resp, _, err := authGet(cfg.BaseURL, "/api/v1/clusters", token)
			if err != nil {
				check(&sr, "redteam:wrong-secret", false, fmt.Sprintf("request error: %v", err), 0)
			} else {
				drainBody(resp)
				check(&sr, "redteam:wrong-secret", resp.StatusCode == 401,
					fmt.Sprintf("expected 401, got %s", statusText(resp)), 0)
			}
		}
	}

	// --- Test 4: No "Bearer " prefix ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:no-bearer-prefix", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			headers := map[string]string{"Authorization": token} // missing "Bearer " prefix
			resp, _, err := doRequest("GET", cfg.BaseURL+"/api/v1/clusters", nil, headers)
			if err != nil {
				check(&sr, "redteam:no-bearer-prefix", false, fmt.Sprintf("request error: %v", err), 0)
			} else {
				drainBody(resp)
				check(&sr, "redteam:no-bearer-prefix", resp.StatusCode == 401,
					fmt.Sprintf("expected 401, got %s", statusText(resp)), 0)
			}
		}
	}

	// --- Test 5: SQL injection in name field ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:sql-injection", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			sqli := `{"name":"'; DROP TABLE clusters; --","provider":"aws","region":"us-east-1"}`
			resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", token,
				strings.NewReader(sqli))
			if err != nil {
				check(&sr, "redteam:sql-injection", true, fmt.Sprintf("connection rejected: %v", err), 0)
			} else {
				drainBody(resp)
				check(&sr, "redteam:sql-injection", resp.StatusCode < 500,
					fmt.Sprintf("status=%s (no 5xx = injection handled)", statusText(resp)), 0)
			}
		}
	}

	// --- Test 6: Path traversal ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:path-traversal", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			paths := []string{
				"/api/v1/clusters/../../etc/passwd",
				"/api/v1/clusters/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
				"/api/v1/clusters/..%252f..%252fetc%252fpasswd",
			}
			allSafe := true
			for _, path := range paths {
				resp, _, err := authGet(cfg.BaseURL, path, token)
				if err != nil {
					continue
				}
				body := readBody(resp)
				if strings.Contains(body, "root:") {
					allSafe = false
					break
				}
			}
			check(&sr, "redteam:path-traversal", allSafe,
				"no path traversal content leaked", 0)
		}
	}

	// --- Test 7: 10KB header ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:10kb-header", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			bigHeader := strings.Repeat("X", 10240)
			headers := map[string]string{
				"Authorization":  "Bearer " + token,
				"X-Custom-Large": bigHeader,
			}
			resp, _, err := doRequest("GET", cfg.BaseURL+"/api/v1/clusters", nil, headers)
			if err != nil {
				check(&sr, "redteam:10kb-header", true,
					fmt.Sprintf("rejected (expected): %v", err), 0)
			} else {
				drainBody(resp)
				// Should either work or reject — not crash (no 5xx).
				check(&sr, "redteam:10kb-header", resp.StatusCode < 500,
					fmt.Sprintf("status=%s", statusText(resp)), 0)
			}
		}
	}

	// --- Test 8: Empty body POST ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:empty-body-post", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", token,
				strings.NewReader(""))
			if err != nil {
				check(&sr, "redteam:empty-body-post", true,
					fmt.Sprintf("connection error: %v", err), 0)
			} else {
				drainBody(resp)
				check(&sr, "redteam:empty-body-post", resp.StatusCode < 500,
					fmt.Sprintf("status=%s (expected 400)", statusText(resp)), 0)
			}
		}
	}

	// --- Test 9: Duplicate registration ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:duplicate-registration", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			name := uniqueID("dup")
			payload := clusterPayload(name)
			// First registration.
			resp1, _, err1 := authPost(cfg.BaseURL, "/api/v1/clusters", token,
				strings.NewReader(payload))
			if err1 != nil {
				check(&sr, "redteam:duplicate-registration", false, fmt.Sprintf("first reg error: %v", err1), 0)
			} else {
				drainBody(resp1)
				// Second registration (duplicate).
				resp2, _, err2 := authPost(cfg.BaseURL, "/api/v1/clusters", token,
					strings.NewReader(payload))
				if err2 != nil {
					check(&sr, "redteam:duplicate-registration", false, fmt.Sprintf("second reg error: %v", err2), 0)
				} else {
					drainBody(resp2)
					// Should be 409 Conflict or 400, not 5xx.
					check(&sr, "redteam:duplicate-registration",
						resp2.StatusCode < 500,
						fmt.Sprintf("status=%s (expected 409 or 400)", statusText(resp2)), 0)
				}
			}
		}
	}

	// --- Test 10: Wrong HTTP methods ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:wrong-methods", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			wrongMethods := []struct {
				method string
				path   string
			}{
				{"DELETE", "/api/v1/clusters"},        // DELETE on collection
				{"PUT", "/api/v1/clusters"},            // PUT not supported
				{"PATCH", "/api/v1/clusters"},          // PATCH not supported
				{"POST", "/healthz"},                   // POST on health check
				{"DELETE", "/api/v1/pools"},             // DELETE on pools
			}
			allHandled := true
			for _, wm := range wrongMethods {
				headers := map[string]string{"Authorization": "Bearer " + token}
				resp, _, err := doRequest(wm.method, cfg.BaseURL+wm.path, nil, headers)
				if err != nil {
					continue
				}
				drainBody(resp)
				if resp.StatusCode >= 500 {
					allHandled = false
				}
			}
			check(&sr, "redteam:wrong-methods", allHandled,
				"no 5xx from wrong HTTP methods", 0)
		}
	}

	// --- Test 11: XSS payload in cluster name ---
	{
		token, err := generateToken(secret, "test-user", "admin", 1*time.Hour)
		if err != nil {
			check(&sr, "redteam:xss-payload", false, fmt.Sprintf("token gen error: %v", err), 0)
		} else {
			xss := `{"name":"<script>alert('xss')</script>","provider":"aws","region":"us-east-1"}`
			resp, _, err := authPost(cfg.BaseURL, "/api/v1/clusters", token,
				strings.NewReader(xss))
			if err != nil {
				check(&sr, "redteam:xss-payload", true,
					fmt.Sprintf("connection error: %v", err), 0)
			} else {
				body := readBody(resp)
				// Response should not reflect the script tag unescaped.
				reflected := strings.Contains(body, "<script>")
				check(&sr, "redteam:xss-payload", !reflected && resp.StatusCode < 500,
					fmt.Sprintf("status=%s reflected=%v", statusText(resp), reflected), 0)
			}
		}
	}

	return sr
}
