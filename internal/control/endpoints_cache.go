package control

import (
	"sync"

	"github.com/Veyal/interceptor/internal/store"
)

// endpoints cache — invalidated when flows change; avoids re-aggregating on every Map tab poll.
type endpointsCache struct {
	mu    sync.Mutex
	key   string
	eps   []store.Endpoint
	note  string
	valid bool
}

func (c *endpointsCache) get(key string) ([]store.Endpoint, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || c.key != key {
		return nil, "", false
	}
	return c.eps, c.note, true
}

func (c *endpointsCache) set(key string, eps []store.Endpoint, note string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key = key
	c.eps = eps
	c.note = note
	c.valid = true
}

func (c *endpointsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
}

func endpointsCacheKey(f store.EndpointFilter) string {
	return f.Host + "\x00" + f.Search + "\x00" + f.SearchScope + "\x00" + f.Tag
}
