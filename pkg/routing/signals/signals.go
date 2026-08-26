// Package signals implements the optional pool-level AI Grid Prometheus
// signal contract. It intentionally accepts only explicitly allowlisted
// gauges and never publishes pod-level dimensions.
package signals

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultMaxResponseBytes int64 = 1 << 20

type Sample struct {
	Name        string
	Labels      map[string]string
	Value       float64
	CollectedAt time.Time
}

type Store interface {
	Samples(ctx context.Context) ([]Sample, error)
}

type Publisher struct {
	Site                    string
	Provider                string
	Store                   Store
	AllowedMetrics          map[string]struct{}
	AllowedPeerFingerprints map[string]struct{}
}

func (p *Publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.authorized(r.TLS) {
		http.Error(w, "authenticated grid peer required", http.StatusForbidden)
		return
	}
	samples, err := p.Store.Samples(r.Context())
	if err != nil {
		http.Error(w, "signal collection unavailable", http.StatusServiceUnavailable)
		return
	}
	collect := requestedMetrics(r.URL.Query()["collect[]"])
	filtered := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if _, ok := p.AllowedMetrics[sample.Name]; !ok || aggregate(sample.Name) || forbiddenLabels(sample.Labels) {
			continue
		}
		if len(collect) > 0 {
			if _, ok := collect[sample.Name]; !ok {
				continue
			}
		}
		sample.Labels = cloneLabels(sample.Labels)
		sample.Labels["grid_site"] = p.Site
		sample.Labels["grid_provider"] = p.Provider
		filtered = append(filtered, sample)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Name != filtered[j].Name {
			return filtered[i].Name < filtered[j].Name
		}
		return labelsString(filtered[i].Labels) < labelsString(filtered[j].Labels)
	})
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	for _, sample := range filtered {
		_, _ = fmt.Fprintf(w, "%s%s %s %d\n", sample.Name, labelsString(sample.Labels), strconv.FormatFloat(sample.Value, 'g', -1, 64), sample.CollectedAt.UnixMilli())
	}
}

func (p *Publisher) authorized(state *tls.ConnectionState) bool {
	if state == nil || len(state.PeerCertificates) == 0 {
		return false
	}
	if len(p.AllowedPeerFingerprints) == 0 {
		return false
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	_, ok := p.AllowedPeerFingerprints[hex.EncodeToString(sum[:])]
	return ok
}

type Client struct {
	HTTP             *http.Client
	MaxResponseBytes int64
	MaxStaleness     time.Duration
}

type Peer struct{ Site, Endpoint string }
type Poller struct {
	Client *Client
	Peers  []Peer
}

func (p *Poller) Poll(ctx context.Context) (map[string][]Sample, error) {
	result := make(map[string][]Sample, len(p.Peers))
	var errs []string
	for _, peer := range p.Peers {
		samples, err := p.Client.Poll(ctx, peer.Endpoint, peer.Site)
		if err != nil {
			errs = append(errs, peer.Site+": "+err.Error())
			continue
		}
		result[peer.Site] = samples
	}
	if len(errs) > 0 {
		return result, fmt.Errorf("grid signal poll errors: %s", strings.Join(errs, "; "))
	}
	return result, nil
}

func NewClient(tlsConfig *tls.Config, timeout, maxStaleness time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxStaleness <= 0 {
		maxStaleness = 30 * time.Second
	}
	return &Client{HTTP: &http.Client{Timeout: timeout, Transport: &http.Transport{TLSClientConfig: tlsConfig}}, MaxResponseBytes: DefaultMaxResponseBytes, MaxStaleness: maxStaleness}
}

func (c *Client) Poll(ctx context.Context, endpoint, expectedSite string) ([]Sample, error) {
	if !strings.HasPrefix(endpoint, "https://") {
		return nil, fmt.Errorf("grid signal endpoint must use HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grid signal endpoint returned %d", resp.StatusCode)
	}
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("grid signal response exceeds %d bytes", limit)
	}
	publishedAt, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return nil, fmt.Errorf("grid signal response missing valid Date header")
	}
	samples, err := parse(body)
	if err != nil {
		return nil, err
	}
	result := samples[:0]
	for _, sample := range samples {
		if sample.Labels["grid_site"] != expectedSite {
			return nil, fmt.Errorf("grid_site does not match authenticated peer")
		}
		age := publishedAt.Sub(sample.CollectedAt)
		if age < 0 || age > c.MaxStaleness {
			continue
		}
		result = append(result, sample)
	}
	return result, nil
}

func parse(body []byte) ([]Sample, error) {
	var result []Sample
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid Prometheus sample %q", line)
		}
		name, labels, err := parseSeries(parts[0])
		if err != nil {
			return nil, err
		}
		value, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, err
		}
		millis, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, err
		}
		result = append(result, Sample{Name: name, Labels: labels, Value: value, CollectedAt: time.UnixMilli(millis)})
	}
	return result, scanner.Err()
}

func parseSeries(raw string) (string, map[string]string, error) {
	open := strings.IndexByte(raw, '{')
	if open < 0 {
		return raw, map[string]string{}, nil
	}
	if !strings.HasSuffix(raw, "}") {
		return "", nil, fmt.Errorf("invalid series %q", raw)
	}
	name := raw[:open]
	labels := map[string]string{}
	for _, pair := range strings.Split(raw[open+1:len(raw)-1], ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return "", nil, fmt.Errorf("invalid label %q", pair)
		}
		value, err := strconv.Unquote(kv[1])
		if err != nil {
			return "", nil, err
		}
		labels[kv[0]] = value
	}
	return name, labels, nil
}

func requestedMetrics(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func aggregate(name string) bool {
	return strings.HasSuffix(name, "_bucket") || strings.HasSuffix(name, "_sum") || strings.HasSuffix(name, "_count")
}
func forbiddenLabels(labels map[string]string) bool {
	for key := range labels {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "pod") || strings.Contains(lower, "container") || strings.Contains(lower, "instance") || strings.Contains(lower, "rank") {
			return true
		}
	}
	return false
}
func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}
func labelsString(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strconv.Quote(labels[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// CertificateFingerprint returns the canonical SHA-256 identity used by the
// publisher's peer allowlist.
func CertificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
