package routing

import (
	"context"
	"sync"
	"time"
)

// SessionBinding maps a session to a cluster with an expiry.
type SessionBinding struct {
	ClusterID string
	ExpiresAt time.Time
}

// SessionAffinityTable maintains session-to-cluster bindings for routing
// multi-turn conversations to the cluster that has their KV cache.
type SessionAffinityTable struct {
	mu       sync.RWMutex
	bindings map[string]SessionBinding
	ttl      time.Duration
}

// NewSessionAffinityTable creates a session table with the given TTL.
func NewSessionAffinityTable(ttl time.Duration) *SessionAffinityTable {
	return &SessionAffinityTable{
		bindings: make(map[string]SessionBinding),
		ttl:      ttl,
	}
}

// Lookup returns the bound cluster for a session, if the binding exists and
// has not expired.
func (t *SessionAffinityTable) Lookup(sessionID string) (clusterID string, found bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	binding, ok := t.bindings[sessionID]
	if !ok || time.Now().After(binding.ExpiresAt) {
		return "", false
	}
	return binding.ClusterID, true
}

// Bind creates or updates a session binding with a fresh TTL.
func (t *SessionAffinityTable) Bind(sessionID, clusterID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bindings[sessionID] = SessionBinding{
		ClusterID: clusterID,
		ExpiresAt: time.Now().Add(t.ttl),
	}
}

// Unbind removes a single session binding.
func (t *SessionAffinityTable) Unbind(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.bindings, sessionID)
}

// UnbindCluster removes all session bindings for a given cluster. Called when
// a cluster is drained to force sessions to reroute.
func (t *SessionAffinityTable) UnbindCluster(clusterID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, binding := range t.bindings {
		if binding.ClusterID == clusterID {
			delete(t.bindings, id)
		}
	}
}

// Len returns the number of active bindings (including expired ones not yet
// reaped).
func (t *SessionAffinityTable) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.bindings)
}

// StartReaper runs a background goroutine that removes expired bindings.
func (t *SessionAffinityTable) StartReaper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.reap()
			}
		}
	}()
}

func (t *SessionAffinityTable) reap() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, binding := range t.bindings {
		if now.After(binding.ExpiresAt) {
			delete(t.bindings, id)
		}
	}
}
