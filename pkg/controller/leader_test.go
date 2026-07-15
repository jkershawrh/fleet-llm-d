package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaderElector_AcquiresNewLease(t *testing.T) {
	var created atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost {
			created.Store(true)
			var lease k8sLease
			json.NewDecoder(r.Body).Decode(&lease)
			lease.Metadata.ResourceVersion = "1"
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(lease)
			return
		}
	}))
	defer srv.Close()

	le := NewLeaderElector(srv.URL, "test", "test-instance")
	le.token = "test-token"

	err := le.createLease(context.Background())
	if err != nil {
		t.Fatalf("createLease: %v", err)
	}
	if !created.Load() {
		t.Error("expected lease creation POST")
	}
}

func TestLeaderElector_DetectsExpiredLease(t *testing.T) {
	expired := time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339Nano)
	lease := &k8sLease{
		Spec: k8sLeaseSpec{
			HolderIdentity:       "other-instance",
			LeaseDurationSeconds: 15,
			RenewTime:            expired,
		},
	}

	le := &LeaderElector{identity: "my-instance"}
	if !le.isExpired(lease) {
		t.Error("expected lease to be expired")
	}
	if le.isHeldByMe(lease) {
		t.Error("expected lease NOT held by me")
	}
}

func TestLeaderElector_DetectsActiveLease(t *testing.T) {
	recent := time.Now().UTC().Format(time.RFC3339Nano)
	lease := &k8sLease{
		Spec: k8sLeaseSpec{
			HolderIdentity:       "other-instance",
			LeaseDurationSeconds: 15,
			RenewTime:            recent,
		},
	}

	le := &LeaderElector{identity: "my-instance"}
	if le.isExpired(lease) {
		t.Error("expected lease to be active")
	}
}

func TestLeaderElector_IsLeaderDefault(t *testing.T) {
	le := &LeaderElector{}
	if le.IsLeader() {
		t.Error("expected IsLeader() to be false by default")
	}
}

func TestLeaderElector_DoesNotCreateLeaseOnAuthorizationFailure(t *testing.T) {
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			creates.Add(1)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	le := NewLeaderElector(srv.URL, "test", "test-instance")
	le.token = "test-token"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = le.Run(ctx)
	if creates.Load() != 0 {
		t.Fatalf("expected no create attempt after GET 403, got %d", creates.Load())
	}
}
