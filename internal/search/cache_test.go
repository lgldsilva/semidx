package search

import (
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestNewQueryCacheDefaults(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(0, 0)
	if c.ttl != 5*time.Minute {
		t.Fatalf("default TTL = %v", c.ttl)
	}
	if c.maxSize != 1000 {
		t.Fatalf("default maxSize = %d", c.maxSize)
	}
	if c.Len() != 0 {
		t.Fatal("new cache should be empty")
	}
}

func TestNewQueryCacheCustom(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(30*time.Second, 42)
	if c.ttl != 30*time.Second || c.maxSize != 42 {
		t.Fatal("custom values not respected")
	}
}

func TestCacheGetMiss(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 10)
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestCacheSetGet(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 10)
	results := []store.SearchResult{{FilePath: "a.go", Score: 1.0}}
	c.Set("q1", results)
	got, ok := c.Get("q1")
	if !ok {
		t.Fatal("expected hit")
	}
	if len(got) != 1 || got[0].FilePath != "a.go" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestCacheExpiry(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(1*time.Millisecond, 10)
	c.Set("q1", []store.SearchResult{{FilePath: "x.go"}})
	time.Sleep(2 * time.Millisecond)
	_, ok := c.Get("q1")
	if ok {
		t.Fatal("expected expired entry to miss")
	}
	if c.Len() != 0 {
		t.Fatalf("expired entry not cleaned, len=%d", c.Len())
	}
}

func TestCacheDelete(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 10)
	c.Set("k1", nil)
	c.Set("k2", nil)
	c.Delete("k1")
	if c.Len() != 1 {
		t.Fatalf("len after delete = %d, want 1", c.Len())
	}
	_, ok := c.Get("k1")
	if ok {
		t.Fatal("k1 should be deleted")
	}
}

func TestCacheEviction(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 2)
	c.Set("a", nil)
	c.Set("b", nil)
	c.Set("c", nil) // should evict "a"
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
	_, ok := c.Get("a")
	if ok {
		t.Fatal("a should have been evicted")
	}
}

func TestCacheInvalidateProject(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 10)
	c.Set("query|42|extra", nil)
	c.Set("query|99|other", nil)
	c.Set("query-no-pipe", nil)
	c.InvalidateProject(42)
	if c.Len() != 2 {
		t.Fatalf("len after invalidate = %d, want 2", c.Len())
	}
	_, ok := c.Get("query|42|extra")
	if ok {
		t.Fatal("project 42 entry should be invalidated")
	}
	_, ok = c.Get("query|99|other")
	if !ok {
		t.Fatal("project 99 entry should remain")
	}
}

func TestCacheConcurrent(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 100)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			c.Set("concurrent", []store.SearchResult{{}})
			c.Get("concurrent")
		}
		close(done)
	}()
	go func() {
		for i := 0; i < 100; i++ {
			c.Delete("concurrent")
			c.Len()
		}
	}()
	<-done
}

func TestContainsProjectID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key       string
		projectID int
		want      bool
	}{
		{"query|42|rest", 42, true},
		{"query|42|rest", 99, false},
		{"no pipes here", 42, false},
		{"x|42", 42, false},      // no trailing pipe
		{"|42|", 42, false},      // no leading content before pipe... actually this has |42| so it matches
		{"", 42, false},
	}
	// |42| test: the function scans for | then checks if the next chars are "42|"
	// "|42|" → at i=0, key[0] = '|', key[0:4] = "|42|" → matches target "|42|" → true
	// Fixed: that's correct behavior.
	tests[4].want = true
	for _, tt := range tests {
		got := containsProjectID(tt.key, tt.projectID)
		if got != tt.want {
			t.Errorf("containsProjectID(%q,%d) = %v, want %v", tt.key, tt.projectID, got, tt.want)
		}
	}
}

func TestEvictOneEmpty(t *testing.T) {
	t.Parallel()
	c := NewQueryCache(time.Minute, 10)
	c.evictOne() // should not panic
}
