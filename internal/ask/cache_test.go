package ask

import (
	"sync"
	"testing"
)

func TestCacheGetMiss(t *testing.T) {
	c := NewCache()
	_, ok := c.Get("unknown.com")
	if ok {
		t.Error("expected cache miss for unknown domain")
	}
}

func TestCacheSetAndGet(t *testing.T) {
	c := NewCache()
	c.Set("example.com", VerdictAllowOnce)

	v, ok := c.Get("example.com")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if v != VerdictAllowOnce {
		t.Errorf("got %q, want %q", v, VerdictAllowOnce)
	}
}

func TestCacheOverwrite(t *testing.T) {
	c := NewCache()
	c.Set("example.com", VerdictBlock)
	c.Set("example.com", VerdictAlwaysAllow)

	v, _ := c.Get("example.com")
	if v != VerdictAlwaysAllow {
		t.Errorf("got %q, want %q", v, VerdictAlwaysAllow)
	}
}

func TestCacheWaitForPending(t *testing.T) {
	c := NewCache()

	// Mark domain as pending (someone is already showing a dialog)
	ch := c.MarkPending("example.com")
	if ch != nil {
		t.Fatal("first MarkPending should return nil (caller is the one showing dialog)")
	}

	// Second caller should get a channel to wait on
	ch = c.MarkPending("example.com")
	if ch == nil {
		t.Fatal("second MarkPending should return a wait channel")
	}

	// Resolve the pending verdict
	c.Set("example.com", VerdictBlock)

	// The wait channel should now be closed
	select {
	case <-ch:
		// good
	default:
		t.Error("expected wait channel to be closed after Set")
	}

	v, ok := c.Get("example.com")
	if !ok || v != VerdictBlock {
		t.Errorf("got (%q, %v), want (%q, true)", v, ok, VerdictBlock)
	}
}

func TestCacheResolveWithoutStoring(t *testing.T) {
	c := NewCache()

	// First caller marks pending
	ch := c.MarkPending("example.com")
	if ch != nil {
		t.Fatal("first MarkPending should return nil")
	}

	// Second caller gets wait channel
	ch = c.MarkPending("example.com")
	if ch == nil {
		t.Fatal("second MarkPending should return a wait channel")
	}

	// Resolve without storing
	c.Resolve("example.com")

	// Wait channel should be closed
	select {
	case <-ch:
	default:
		t.Error("expected wait channel to be closed after Resolve")
	}

	// But the verdict should NOT be cached
	_, ok := c.Get("example.com")
	if ok {
		t.Error("expected no cached verdict after Resolve")
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	c := NewCache()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			domain := "example.com"
			c.Set(domain, VerdictBlock)
			c.Get(domain)
		}(i)
	}

	wg.Wait()
}
