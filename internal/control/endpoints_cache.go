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
	mu            sync.Mutex
	items         map[string]endpointsCacheEntry
	order         []string // LRU order, oldest first
	debounceTimer *time.Timer
	// gen counts invalidations. A caller reads it (generation) before it runs an
	// aggregation and hands it back to setAt, which drops a result computed
	// before an invalidation that landed while the query was running.
	gen uint64
	// bulkGen counts immediate invalidations only (import, merge, delete, prune,
	// prune undo): the changes that can add or remove flows anywhere in the id
	// range, unlike live capture, which only appends. See bulkGeneration.
	bulkGen uint64
	// afterQuery, when set by a test, runs between listEndpoints' aggregation and
	// its cache write, to interleave an invalidation there.
	afterQuery func()
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

// generation returns the current invalidation count; read it before computing a
// value for setAt.
func (c *endpointsCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// setAt stores a value computed while the cache was at generation gen. It is a
// no-op when the cache was invalidated since: the aggregation ran outside the
// lock, so its result may predate the change that invalidated the cache, and
// storing it would serve stale endpoints until the next invalidation.
func (c *endpointsCache) setAt(gen uint64, key string, eps []store.Endpoint, note string, total int, truncated bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return false
	}
	c.store(key, eps, note, total, truncated)
	return true
}

// store writes one entry; the caller holds c.mu.
func (c *endpointsCache) store(key string, eps []store.Endpoint, note string, total int, truncated bool) {
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
	c.gen++
	c.bulkGen++
	c.items = nil
	c.order = nil
}

// bulkGeneration identifies the current set of non-append flow changes. A value
// derived from history that only needs topping up with newly captured flows
// (hasInScopeTraffic) is valid while it is unchanged.
func (c *endpointsCache) bulkGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bulkGen
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
		c.gen++
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
