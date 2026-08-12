package ask

import "sync"

const defaultVerdictCacheEntries = 4096

// Cache stores a bounded set of session-level verdict decisions for endpoints.
// It is safe for concurrent use.
type Cache struct {
	mu         sync.Mutex
	verdicts   map[string]string
	pending    map[string]chan struct{}
	order      []string
	orderIndex int
	maxEntries int
}

// NewCache creates a new empty verdict cache.
func NewCache() *Cache {
	return newCache(defaultVerdictCacheEntries)
}

func newCache(maxEntries int) *Cache {
	return &Cache{
		verdicts:   make(map[string]string),
		pending:    make(map[string]chan struct{}),
		order:      make([]string, 0, max(0, maxEntries)),
		maxEntries: maxEntries,
	}
}

// Get returns the cached verdict for a decision key.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.verdicts[key]
	return v, ok
}

// Set stores a verdict and closes any pending wait channel for the key.
func (c *Cache) Set(key string, verdict string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.verdicts[key]; exists {
		c.verdicts[key] = verdict
	} else if c.maxEntries > 0 {
		if len(c.order) < c.maxEntries {
			c.order = append(c.order, key)
		} else {
			expired := c.order[c.orderIndex]
			delete(c.verdicts, expired)
			c.order[c.orderIndex] = key
			c.orderIndex = (c.orderIndex + 1) % c.maxEntries
		}
		c.verdicts[key] = verdict
	}
	if ch, ok := c.pending[key]; ok {
		close(ch)
		delete(c.pending, key)
	}
}

// MarkPending marks a decision key as having a dialog in progress.
// Returns nil if this caller is the first (they should show the dialog).
// Returns a channel if another caller is already showing the dialog (wait on it).
func (c *Cache) MarkPending(key string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.pending[key]; ok {
		return ch // already pending, wait on existing channel
	}
	ch := make(chan struct{})
	c.pending[key] = ch
	return nil // first caller, show dialog
}

// Resolve closes any pending wait channel for a key without storing a verdict.
// Use this for allow-once verdicts that should not be cached.
func (c *Cache) Resolve(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.pending[key]; ok {
		close(ch)
		delete(c.pending, key)
	}
}
