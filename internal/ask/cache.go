package ask

import "sync"

// Cache stores session-level verdict decisions for domains.
// It is safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	verdicts map[string]string
	pending  map[string]chan struct{}
}

// NewCache creates a new empty verdict cache.
func NewCache() *Cache {
	return &Cache{
		verdicts: make(map[string]string),
		pending:  make(map[string]chan struct{}),
	}
}

// Get returns the cached verdict for a domain.
func (c *Cache) Get(domain string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.verdicts[domain]
	return v, ok
}

// Set stores a verdict and closes any pending wait channel for the domain.
func (c *Cache) Set(domain string, verdict string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verdicts[domain] = verdict
	if ch, ok := c.pending[domain]; ok {
		close(ch)
		delete(c.pending, domain)
	}
}

// MarkPending marks a domain as having a dialog in progress.
// Returns nil if this caller is the first (they should show the dialog).
// Returns a channel if another caller is already showing the dialog (wait on it).
func (c *Cache) MarkPending(domain string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.pending[domain]; ok {
		return ch // already pending, wait on existing channel
	}
	ch := make(chan struct{})
	c.pending[domain] = ch
	return nil // first caller, show dialog
}

// Resolve closes any pending wait channel for a domain without storing a verdict.
// Use this for allow-once verdicts that should not be cached.
func (c *Cache) Resolve(domain string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.pending[domain]; ok {
		close(ch)
		delete(c.pending, domain)
	}
}
