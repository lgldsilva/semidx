package search

import (
	"fmt"
	"sync"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

// cacheEntry holds cached search results and their expiry.
type cacheEntry struct {
	results   []store.SearchResult
	expiresAt time.Time
}

// QueryCache is an LRU-ish cache for search results with TTL expiry.
// It is safe for concurrent use.
type QueryCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	maxSize int
	keys    []string // FIFO eviction order
}

// NewQueryCache creates a QueryCache with the given TTL and max entry count.
func NewQueryCache(ttl time.Duration, maxSize int) *QueryCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &QueryCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Get returns cached results if a fresh entry exists.
func (c *QueryCache) Get(key string) ([]store.SearchResult, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		c.Delete(key)
		return nil, false
	}
	return entry.results, true
}

// Set stores results under the given key.
func (c *QueryCache) Set(key string, results []store.SearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := &cacheEntry{
		results:   results,
		expiresAt: time.Now().Add(c.ttl),
	}

	// Updating an existing key must not append to the FIFO list again: doing so
	// grew c.keys unboundedly and let evictOne drop a still-live entry whose
	// key had a duplicate later in the list.
	if _, exists := c.entries[key]; exists {
		c.entries[key] = entry
		return
	}

	// Evict oldest if at capacity.
	if len(c.entries) >= c.maxSize {
		c.evictOne()
	}

	c.entries[key] = entry
	c.keys = append(c.keys, key)
}

// Delete removes a single key from the cache.
func (c *QueryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	// Remove from FIFO list (linear scan — fine at cache scale).
	for i, k := range c.keys {
		if k == key {
			c.keys = append(c.keys[:i], c.keys[i+1:]...)
			break
		}
	}
}

// InvalidateProject removes all entries whose key contains the project ID.
// Called when a project is re-indexed.
func (c *QueryCache) InvalidateProject(projectID int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if containsProjectID(key, projectID) {
			delete(c.entries, key)
		}
	}
	// Rebuild the FIFO list without removed keys.
	fresh := make([]string, 0, len(c.entries))
	for _, k := range c.keys {
		if _, ok := c.entries[k]; ok {
			fresh = append(fresh, k)
		}
	}
	c.keys = fresh
}

// Len returns the current number of entries (for testing).
func (c *QueryCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// evictOne removes the oldest entry (FIFO eviction).
func (c *QueryCache) evictOne() {
	if len(c.keys) == 0 {
		return
	}
	oldest := c.keys[0]
	delete(c.entries, oldest)
	c.keys = c.keys[1:]
}

// containsProjectID checks whether the cache key references projectID.
func containsProjectID(key string, projectID int) bool {
	// The project ID is the second field (after query, between pipes).
	// We scan for the pattern |<projectID>|.
	target := fmt.Sprintf("|%d|", projectID)
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			if i+len(target) <= len(key) && key[i:i+len(target)] == target {
				return true
			}
		}
	}
	return false
}
