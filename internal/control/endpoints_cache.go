package control

import (
	"strconv"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// endpoints cache — a small LRU keyed by filter. Toggling between "All domains"
// and a specific host (or any filter change) used to re-run the full
// GROUP BY aggregation because only one entry was cached.
//
// Two kinds of change reach the cache:
//
//   - A bulk change (import, merge, delete, prune, prune undo) can add or remove
//     flows anywhere. invalidate drops every entry at once, and a result whose
//     aggregation started before it is refused by setAt.
//   - Live traffic (a captured or updated flow, a tag change) only makes an
//     entry out of date. invalidateDebounced records the time; an entry whose
//     aggregation started before that is "stale", not gone.
//
// A stale entry is served, without re-aggregating, only while traffic is
// still arriving (a capture within endpointsTrafficQuiet) and until its
// refresh interval has passed since it was computed: endpointsRefreshFactor
// times what the aggregation cost, at least endpointsMinRefresh and at most
// endpointsMaxRefresh. The first request after that gets a miss and
// re-aggregates; requests arriving while it does are served the stale entry
// rather than each running the same aggregation. No entry is ever served more
// than endpointsMaxAge after its aggregation began. Once traffic pauses the next
// request re-aggregates at once: the Map polls after captures, so the poll
// that follows the last capture of a burst must see it, as no later one comes.
// The minimum interval is below the Map's two-second poll throttle, so a
// project whose aggregate is fast is refreshed on every poll, as before.
//
// Before, traffic cleared the whole cache every two seconds and a result was
// refused whenever a capture landed during its aggregation. On a History whose
// aggregate takes longer than two seconds that meant no result was ever
// cached: every Map poll of every client ran the full aggregate, concurrently.
type endpointsCache struct {
	mu    sync.Mutex
	items map[string]endpointsCacheEntry
	order []string // LRU order, oldest first
	// gen counts bulk invalidations. generation hands out a token bound to it
	// (and to the time the aggregation starts); setAt refuses a token from
	// before an invalidation that landed while the query was running.
	gen uint64
	// bulkGen counts immediate invalidations only (import, merge, delete, prune,
	// prune undo): the changes that can add or remove flows anywhere in the id
	// range, unlike live capture, which only appends. See bulkGeneration.
	bulkGen uint64
	// lastCapture is when live traffic last changed History.
	lastCapture time.Time
	// tokens are the aggregations in flight, by the value generation returned.
	tokens    map[uint64]endpointsCacheToken
	nextToken uint64
	// afterQuery, when set by a test, runs between listEndpoints' aggregation and
	// its cache write, to interleave an invalidation there.
	afterQuery func()
	// now, when set by a test, replaces time.Now.
	now func() time.Time
}

type endpointsCacheToken struct {
	gen     uint64
	started time.Time
}

type endpointsCacheEntry struct {
	eps       []store.Endpoint
	note      string
	total     int
	truncated bool
	// started is when the aggregation that produced the entry began: the entry
	// reflects History no later than this.
	started time.Time
	// done is when it finished; the entry's age is counted from here.
	done time.Time
	// cost is how long that aggregation took.
	cost time.Duration
	// refreshing is when a request was last told to re-aggregate this entry
	// (zero: none in flight).
	refreshing time.Time
}

const (
	endpointsCacheMax = 8
	// endpointsRefreshFactor bounds the share of time spent re-aggregating one
	// filter under constant traffic to 1/endpointsRefreshFactor.
	endpointsRefreshFactor = 5
	// endpointsMinRefresh is the shortest refresh interval. It is under the
	// Map's two-second poll throttle, so one client polling a fast aggregate
	// gets a fresh result every time; it spares the aggregate only for
	// several clients polling at once.
	endpointsMinRefresh = time.Second
	// endpointsTrafficQuiet is how long after the last capture traffic counts
	// as still flowing.
	endpointsTrafficQuiet = 500 * time.Millisecond
	// endpointsMaxRefresh caps the refresh interval however slow the aggregate.
	endpointsMaxRefresh = 15 * time.Second
	// endpointsMaxAge is the oldest data that is ever served, counted from the
	// start of the aggregation that read it. It is also how long a refresh
	// may run before another request retries it.
	endpointsMaxAge = 30 * time.Second
	// endpointsMaxTokens bounds the in-flight table when aggregations fail
	// (a failed one never reaches setAt).
	endpointsMaxTokens = 64
)

func (c *endpointsCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *endpointsCache) get(key string) ([]store.Endpoint, string, int, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, "", 0, false, false
	}
	if !c.servableLocked(key, &e) {
		return nil, "", 0, false, false
	}
	// Move to most-recently-used so frequently-toggled filters stay resident.
	c.touch(key)
	return e.eps, e.note, e.total, e.truncated, true
}

// servableLocked reports whether e may answer a request now. When it returns
// false for an entry that is merely due for a refresh, it records that this
// caller is refreshing it, so the requests behind it are served the stale
// entry instead of repeating the aggregation.
func (c *endpointsCache) servableLocked(key string, e *endpointsCacheEntry) bool {
	if c.lastCapture.Before(e.started) {
		return true // nothing was captured since the aggregation began
	}
	now := c.clock()
	age := now.Sub(e.done)
	// The bound is on the data's age, counted from when its aggregation began.
	if now.Sub(e.started) > endpointsMaxAge || now.Sub(c.lastCapture) > endpointsTrafficQuiet {
		return false
	}
	if age < endpointsRefreshInterval(e.cost) {
		return true
	}
	if !e.refreshing.IsZero() && now.Sub(e.refreshing) < endpointsMaxAge {
		return true // another request is already re-aggregating
	}
	e.refreshing = now
	c.items[key] = *e
	return false
}

// endpointsRefreshInterval is how long a stale entry is served before it is
// re-aggregated, given what the aggregation cost.
func endpointsRefreshInterval(cost time.Duration) time.Duration {
	return min(max(endpointsRefreshFactor*cost, endpointsMinRefresh), endpointsMaxRefresh)
}

// generation starts an aggregation: it returns a token to hand back to setAt
// with the result. Read it before computing the value.
func (c *endpointsCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens == nil {
		c.tokens = map[uint64]endpointsCacheToken{}
	}
	if len(c.tokens) >= endpointsMaxTokens {
		c.dropOldestTokenLocked()
	}
	c.nextToken++
	c.tokens[c.nextToken] = endpointsCacheToken{gen: c.gen, started: c.clock()}
	return c.nextToken
}

func (c *endpointsCache) dropOldestTokenLocked() {
	oldest := uint64(0)
	for id := range c.tokens {
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	delete(c.tokens, oldest)
}

// setAt stores a value computed under the token generation returned. It is a
// no-op when the cache was invalidated since: the aggregation ran outside the
// lock, so its result may predate the change that invalidated the cache, and
// storing it would serve stale endpoints until the next invalidation. Traffic
// captured while the aggregation ran does not refuse the result; the entry is
// simply stale from the start and refreshed on the usual interval.
func (c *endpointsCache) setAt(token uint64, key string, eps []store.Endpoint, note string, total int, truncated bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tokens[token]
	delete(c.tokens, token)
	if !ok || t.gen != c.gen {
		return false
	}
	done := c.clock()
	c.store(key, endpointsCacheEntry{
		eps: eps, note: note, total: total, truncated: truncated,
		started: t.started, done: done, cost: done.Sub(t.started),
	})
	return true
}

// store writes one entry; the caller holds c.mu.
func (c *endpointsCache) store(key string, e endpointsCacheEntry) {
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
	c.items[key] = e
}

func (c *endpointsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.bulkGen++
	c.items = nil
	c.order = nil
	c.tokens = nil
}

// bulkGeneration identifies the current set of non-append flow changes. A value
// derived from history that only needs topping up with newly captured flows
// (hasInScopeTraffic) is valid while it is unchanged.
func (c *endpointsCache) bulkGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bulkGen
}

// invalidateDebounced notes that live traffic changed History. It is called
// for every captured flow, so it only records the time: the entries become
// stale and are refreshed on their interval (see endpointsCache) instead of
// being dropped.
func (c *endpointsCache) invalidateDebounced() {
	now := c.clock()
	c.mu.Lock()
	c.lastCapture = now
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
