package classifier

import (
	"context"
	"sync"
	"time"
)

type cachedResult struct {
	Result    *ClassifyResult
	ExpiresAt time.Time
}

type ClassificationCache struct {
	mu      sync.RWMutex
	entries map[string]cachedResult
	ttl     time.Duration
}

func NewClassificationCache(ttl time.Duration) *ClassificationCache {
	return &ClassificationCache{
		entries: make(map[string]cachedResult),
		ttl:     ttl,
	}
}

func (c *ClassificationCache) Get(text string) (*ClassifyResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[text]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Result, true
}

func (c *ClassificationCache) Put(text string, result *ClassifyResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[text] = cachedResult{
		Result:    result,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

func (c *ClassificationCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *ClassificationCache) StartReaper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.reap()
			}
		}
	}()
}

func (c *ClassificationCache) reap() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
		}
	}
}
