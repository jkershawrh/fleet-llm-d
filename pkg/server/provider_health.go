package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/routing/policy"
)

type providerHealthEntry struct {
	healthy              bool
	checkedAt            time.Time
	consecutiveSuccesses int
	consecutiveFailures  int
}

const (
	providerHealthyThreshold   = 2
	providerUnhealthyThreshold = 3
)

// ProviderHealthCache actively verifies inference endpoints. Configured
// providers fail closed: repository or agent health cannot override a failed
// inference probe.
type ProviderHealthCache struct {
	mu      sync.Mutex
	urls    map[string]string
	entries map[string]providerHealthEntry
	ttl     time.Duration
	client  *http.Client
	now     func() time.Time
}

// Ready reports whether every configured provider has completed enough probe
// cycles to make an authoritative healthy/unhealthy decision. Until then an
// inference replica must remain out of Service endpoints: accepting traffic
// with an empty health cache creates a cold-start no-compatible-capacity
// window during rolling updates and preemption recovery.
func (p *ProviderHealthCache) Ready() bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for clusterID := range p.urls {
		entry, ok := p.entries[clusterID]
		if !ok || entry.consecutiveSuccesses < providerHealthyThreshold && entry.consecutiveFailures < providerUnhealthyThreshold {
			return false
		}
	}
	return true
}

func NewProviderHealthCache(urls map[string]string, caPath string) (*ProviderHealthCache, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caPath != "" {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read provider CA bundle: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("provider CA bundle contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	return &ProviderHealthCache{
		urls: urls, entries: make(map[string]providerHealthEntry), ttl: 5 * time.Second,
		client: &http.Client{Transport: transport, Timeout: 2 * time.Second}, now: time.Now,
	}, nil
}

func (p *ProviderHealthCache) Healthy(ctx context.Context, clusterID string) (bool, bool) {
	if p == nil {
		return false, false
	}
	url, configured := p.urls[clusterID]
	if !configured {
		return false, false
	}
	now := p.now()
	p.mu.Lock()
	entry, cached := p.entries[clusterID]
	if cached && now.Sub(entry.checkedAt) < p.ttl {
		p.mu.Unlock()
		return entry.healthy, true
	}
	p.mu.Unlock()

	return p.probe(ctx, clusterID, url), true
}

// Snapshot returns the last authoritative probe state without initiating a
// request. Routing publishers use the probe timestamp as provider freshness so
// an actively checked static provider does not become stale merely because its
// inventory record is immutable during certification.
func (p *ProviderHealthCache) Snapshot(clusterID string) (healthy, configured bool, checkedAt time.Time) {
	if p == nil {
		return false, false, time.Time{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, configured = p.urls[clusterID]; !configured {
		return false, false, time.Time{}
	}
	entry, ok := p.entries[clusterID]
	if !ok {
		return false, true, time.Time{}
	}
	return entry.healthy, true, entry.checkedAt
}

func (p *ProviderHealthCache) probe(ctx context.Context, clusterID, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	probeSucceeded := false
	statusCode := 0
	probeErr := err
	if err == nil {
		resp, requestErr := p.client.Do(req)
		probeErr = requestErr
		if requestErr == nil {
			statusCode = resp.StatusCode
			probeSucceeded = resp.StatusCode >= 200 && resp.StatusCode < 300
			_ = resp.Body.Close()
		}
	}
	p.mu.Lock()
	entry := p.entries[clusterID]
	wasHealthy := entry.healthy
	entry.checkedAt = p.now()
	if probeSucceeded {
		entry.consecutiveSuccesses++
		entry.consecutiveFailures = 0
		if entry.consecutiveSuccesses >= providerHealthyThreshold {
			entry.healthy = true
		}
	} else {
		entry.consecutiveFailures++
		entry.consecutiveSuccesses = 0
		if entry.consecutiveFailures >= providerUnhealthyThreshold {
			entry.healthy = false
		}
	}
	p.entries[clusterID] = entry
	p.mu.Unlock()
	if probeSucceeded && entry.healthy && !wasHealthy {
		slog.Info("inference provider health recovered", "provider", clusterID, "url", url)
	}
	if !probeSucceeded && (entry.consecutiveFailures == 1 || (wasHealthy && !entry.healthy)) {
		attrs := []any{"provider", clusterID, "url", url, "status", statusCode, "consecutive_failures", entry.consecutiveFailures}
		if probeErr != nil {
			attrs = append(attrs, "error", probeErr)
		}
		slog.Warn("inference provider health probe failed", attrs...)
	}
	return entry.healthy
}

func (p *ProviderHealthCache) probeAll(ctx context.Context) {
	for clusterID, url := range p.urls {
		probeCtx, cancel := context.WithTimeout(ctx, p.client.Timeout)
		p.probe(probeCtx, clusterID, url)
		cancel()
	}
}

// Initialize blocks process startup until every configured provider has an
// authoritative probe state. This prevents a restarted process from binding
// its serving socket while its in-memory provider cache is still empty.
func (p *ProviderHealthCache) Initialize(ctx context.Context) error {
	if p == nil {
		return nil
	}
	for {
		p.probeAll(ctx)
		if p.Ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("initialize provider health: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// Start continuously probes every configured provider. Routing therefore
// converges even when no client requests are arriving.
func (p *ProviderHealthCache) Start(ctx context.Context) {
	if p == nil {
		return
	}
	go func() {
		p.probeAll(ctx)
		ticker := time.NewTicker(p.ttl)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.probeAll(ctx)
			}
		}
	}()
}

func (fc *FleetController) BuildInferenceClusterHealth(ctx context.Context) []policy.ClusterHealth {
	health := fc.BuildClusterHealth(ctx)
	for i := range health {
		if activelyHealthy, configured := fc.ProviderHealth.Healthy(ctx, health[i].ClusterID); configured {
			health[i].Healthy = health[i].Healthy && activelyHealthy
		}
	}
	return health
}
