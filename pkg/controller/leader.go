package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
)

const (
	leaseNamespace     = "fleet-llm-d"
	leaseName          = "fleet-controller-leader"
	leaseDurationSecs  = 15
	leaseRenewInterval = 5 * time.Second
	leaseRetryInterval = 3 * time.Second
	serviceAccountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// LeaderElector implements leader election using Kubernetes Lease objects
// via raw HTTPS (no client-go dependency).
type LeaderElector struct {
	apiServer string
	namespace string
	token     string
	identity  string
	http      *http.Client
	isLeader  atomic.Bool
}

// NewLeaderElector creates a leader elector that uses the K8s Lease API.
// identity should be unique per controller instance (e.g., pod name).
func NewLeaderElector(apiServer, namespace, identity string) *LeaderElector {
	if namespace == "" {
		namespace = leaseNamespace
	}

	token := os.Getenv("KUBE_TOKEN")
	if token == "" {
		if data, err := os.ReadFile(serviceAccountPath + "/token"); err == nil {
			token = strings.TrimSpace(string(data))
		}
	}

	tlsOptions := tlsutil.TLSOptions{}
	if _, err := os.Stat(serviceAccountPath + "/ca.crt"); err == nil {
		tlsOptions.CAPath = serviceAccountPath + "/ca.crt"
	}
	tlsConfig, err := tlsutil.NewTLSConfig(tlsOptions)
	if err != nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	tlsConfig.MinVersion = tls.VersionTLS13

	return &LeaderElector{
		apiServer: strings.TrimRight(apiServer, "/"),
		namespace: namespace,
		identity:  identity,
		token:     token,
		http:      &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}},
	}
}

// IsLeader returns whether this instance currently holds the lease.
func (le *LeaderElector) IsLeader() bool {
	return le.isLeader.Load()
}

// Run starts the leader election loop. Blocks until context is cancelled.
func (le *LeaderElector) Run(ctx context.Context) error {
	log.Printf("leader election started (identity=%s, namespace=%s)", le.identity, le.namespace)

	for {
		select {
		case <-ctx.Done():
			le.isLeader.Store(false)
			return ctx.Err()
		default:
		}

		lease, err := le.getLease(ctx)
		if err != nil {
			if err := le.createLease(ctx); err != nil {
				log.Printf("leader election: failed to create lease: %v", err)
				time.Sleep(leaseRetryInterval)
				continue
			}
			le.isLeader.Store(true)
			log.Printf("leader election: acquired lease (new)")
			le.renewLoop(ctx)
			continue
		}

		if le.isHeldByMe(lease) {
			le.isLeader.Store(true)
			le.renewLoop(ctx)
			continue
		}

		if le.isExpired(lease) {
			if err := le.acquireLease(ctx, lease); err != nil {
				log.Printf("leader election: failed to acquire expired lease: %v", err)
				time.Sleep(leaseRetryInterval)
				continue
			}
			le.isLeader.Store(true)
			log.Printf("leader election: acquired expired lease")
			le.renewLoop(ctx)
			continue
		}

		le.isLeader.Store(false)
		time.Sleep(leaseRetryInterval)
	}
}

func (le *LeaderElector) renewLoop(ctx context.Context) {
	ticker := time.NewTicker(leaseRenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := le.renewLease(ctx); err != nil {
				log.Printf("leader election: lost lease: %v", err)
				le.isLeader.Store(false)
				return
			}
		}
	}
}

type k8sLease struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   k8sLeaseMetadata  `json:"metadata"`
	Spec       k8sLeaseSpec      `json:"spec"`
}

type k8sLeaseMetadata struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

type k8sLeaseSpec struct {
	HolderIdentity       string `json:"holderIdentity"`
	LeaseDurationSeconds int    `json:"leaseDurationSeconds"`
	AcquireTime          string `json:"acquireTime,omitempty"`
	RenewTime            string `json:"renewTime,omitempty"`
}

func (le *LeaderElector) leaseURL() string {
	return fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases/%s",
		le.apiServer, le.namespace, leaseName)
}

func (le *LeaderElector) getLease(ctx context.Context) (*k8sLease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, le.leaseURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+le.token)

	resp, err := le.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("lease not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get lease: %d %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	var lease k8sLease
	if err := json.Unmarshal(body, &lease); err != nil {
		return nil, fmt.Errorf("unmarshal lease: %w", err)
	}
	return &lease, nil
}

func (le *LeaderElector) createLease(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lease := k8sLease{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata:   k8sLeaseMetadata{Name: leaseName, Namespace: le.namespace},
		Spec: k8sLeaseSpec{
			HolderIdentity:       le.identity,
			LeaseDurationSeconds: leaseDurationSecs,
			AcquireTime:          now,
			RenewTime:            now,
		},
	}
	return le.writeLease(ctx, http.MethodPost,
		fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases", le.apiServer, le.namespace),
		&lease)
}

func (le *LeaderElector) acquireLease(ctx context.Context, existing *k8sLease) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	existing.Spec.HolderIdentity = le.identity
	existing.Spec.AcquireTime = now
	existing.Spec.RenewTime = now
	return le.writeLease(ctx, http.MethodPut, le.leaseURL(), existing)
}

func (le *LeaderElector) renewLease(ctx context.Context) error {
	lease, err := le.getLease(ctx)
	if err != nil {
		return err
	}
	if lease.Spec.HolderIdentity != le.identity {
		return fmt.Errorf("lease held by %s, not %s", lease.Spec.HolderIdentity, le.identity)
	}
	lease.Spec.RenewTime = time.Now().UTC().Format(time.RFC3339Nano)
	return le.writeLease(ctx, http.MethodPut, le.leaseURL(), lease)
}

func (le *LeaderElector) writeLease(ctx context.Context, method, url string, lease *k8sLease) error {
	data, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+le.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := le.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("%s lease: %d %s", method, resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	return nil
}

func (le *LeaderElector) isHeldByMe(lease *k8sLease) bool {
	return lease.Spec.HolderIdentity == le.identity
}

func (le *LeaderElector) isExpired(lease *k8sLease) bool {
	renewTime, err := time.Parse(time.RFC3339Nano, lease.Spec.RenewTime)
	if err != nil {
		return true
	}
	return time.Since(renewTime) > time.Duration(lease.Spec.LeaseDurationSeconds)*time.Second
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
