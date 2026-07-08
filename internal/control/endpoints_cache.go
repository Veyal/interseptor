package control

import (
	"strconv"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// endpoints cache — a small LRU keyed by filter. Toggling between "All domains"
// and a specific host (or any filter change) used to re-run the full
// GROUP BY aggregation because only one entry was cached. The cache is
// invalidated (cleared) whenever flows change.
type endpointsCache struct {
	mu              sync.Mutex
	items           map[string]endpointsCacheEntry
	order           []string // LRU order, oldest first
	debounceTimer   *time.Timer
}

type endpointsCacheEntry struct {
	eps       []store.Endpoint
	note      string
	total     int
	truncated bool
}

const endpointsCacheMax = 8

func (c *endpointsCache) get(key string) ([]store.Endpoint, string, int, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, "", 0, false, false
	}
	// Move to most-recently-used so frequently-toggled filters stay resident.
	c.touch(key)
	return e.eps, e.note, e.total, e.truncated, true
}

func (c *endpointsCache) set(key string, eps []store.Endpoint, note string, total int, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]endpointsCacheEntry{}
	}
	if _, ok := c.items[key]; !ok {
		c.order = append(c.order, key)
		if len(c.order) > endpointsCacheMax {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
	} else {
		c.touch(key)
	}
	c.items[key] = endpointsCacheEntry{eps: eps, note: note, total: total, truncated: truncated}
}

func (c *endpointsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.debounceTimer != nil {
		c.debounceTimer.Stop()
		c.debounceTimer = nil
	}
	c.items = nil
	c.order = nil
}

// invalidateDebounced clears the cache after a short quiet period so high traffic
// does not invalidate on every single captured flow.
func (c *endpointsCache) invalidateDebounced() {
	c.mu.Lock()
	if c.debounceTimer != nil {
		c.mu.Unlock()
		return
	}
	c.debounceTimer = time.AfterFunc(2*time.Second, func() {
		c.mu.Lock()
		c.debounceTimer = nil
		c.items = nil
		c.order = nil
		c.mu.Unlock()
	})
	c.mu.Unlock()
}

func (c *endpointsCache) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

func endpointsCacheKey(f store.EndpointFilter) string {
	return f.Host + "\x00" + f.Search + "\x00" + f.SearchScope + "\x00" + f.Tag + "\x00" + strconv.FormatBool(f.HideNoiseOnly)
}
