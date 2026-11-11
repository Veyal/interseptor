package control

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// r3Clock is a settable clock for endpointsCache.
type r3Clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *r3Clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *r3Clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func r3CacheWithClock() (*endpointsCache, *r3Clock) {
	clk := &r3Clock{t: time.Unix(1_800_000_000, 0)}
	return &endpointsCache{now: clk.now}, clk
}

// r3Aggregate plays one listEndpoints miss: start, take `cost`, store.
func r3Aggregate(t *testing.T, c *endpointsCache, clk *r3Clock, key, host string, cost time.Duration) {
	t.Helper()
	token := c.generation()
	clk.add(cost)
	if !c.setAt(token, key, []store.Endpoint{{Host: host}}, "", 1, false) {
		t.Fatal("setAt refused a result with no bulk invalidation in between")
	}
}

func r3Hit(c *endpointsCache, key string) (string, bool) {
	eps, _, _, _, ok := c.get(key)
	if !ok || len(eps) == 0 {
		return "", ok
	}
	return eps[0].Host, true
}

// Under constant traffic an entry is re-aggregated once per refresh interval,
// not once per request, and the interval follows the aggregation's cost.
func TestR3EndpointsCacheServesStaleWithinRefreshInterval(t *testing.T) {
	for _, tc := range []struct {
		cost, interval time.Duration
	}{
		{50 * time.Millisecond, endpointsMinRefresh},
		{2 * time.Second, 10 * time.Second},
		{20 * time.Second, endpointsMaxRefresh},
	} {
		c, clk := r3CacheWithClock()
		r3Aggregate(t, c, clk, "k", "a.example.com", tc.cost)
		misses := 0
		// A capture and a Map poll every 100 ms for one minute.
		for i := 0; i < 600; i++ {
			clk.add(100 * time.Millisecond)
			c.invalidateDebounced()
			if _, ok := r3Hit(c, "k"); !ok {
				misses++
				r3Aggregate(t, c, clk, "k", "a.example.com", tc.cost)
			}
		}
		// The clock stands still for the 600 polls only; each aggregation
		// adds its own cost on top.
		want := int(60 * time.Second / tc.interval)
		if misses < want-1 || misses > want+1 {
			t.Errorf("cost %v: 600 polls over 60s of traffic ran %d aggregations, want about %d (one per %v)", tc.cost, misses, want, tc.interval)
		}
	}
}

// With no traffic an entry stays valid however old it is.
func TestR3EndpointsCacheKeepsEntryWithoutTraffic(t *testing.T) {
	c, clk := r3CacheWithClock()
	c.invalidateDebounced() // a capture before the aggregation
	clk.add(time.Millisecond)
	r3Aggregate(t, c, clk, "k", "a.example.com", time.Second)
	clk.add(24 * time.Hour)
	if _, ok := r3Hit(c, "k"); !ok {
		t.Fatal("an entry was dropped although nothing was captured after it was computed")
	}
}

// Once traffic pauses, the next request re-aggregates even inside the refresh
// interval: the Map's last poll of a burst must see the burst's last flow.
func TestR3EndpointsCacheRefreshesWhenTrafficPauses(t *testing.T) {
	c, clk := r3CacheWithClock()
	r3Aggregate(t, c, clk, "k", "a.example.com", 2*time.Second) // refresh interval 10s
	for i := 0; i < 10; i++ {
		clk.add(100 * time.Millisecond)
		c.invalidateDebounced()
		if _, ok := r3Hit(c, "k"); !ok {
			t.Fatalf("poll %d during traffic re-aggregated inside the refresh interval", i)
		}
	}
	clk.add(endpointsTrafficQuiet + time.Millisecond)
	if _, ok := r3Hit(c, "k"); ok {
		t.Fatal("a request after traffic paused was served an aggregate that misses the last captures")
	}
	r3Aggregate(t, c, clk, "k", "a.example.com", 2*time.Second)
	clk.add(time.Hour)
	if _, ok := r3Hit(c, "k"); !ok {
		t.Fatal("the aggregate computed after the pause was not kept")
	}
}

// A stale entry is never served once it is older than endpointsMaxAge, also
// while a refresh is supposedly in flight (a refresh that died).
func TestR3EndpointsCacheNeverServesPastMaxAge(t *testing.T) {
	c, clk := r3CacheWithClock()
	computed := clk.now() // the aggregation starts reading here
	r3Aggregate(t, c, clk, "k", "a.example.com", 3*time.Second)
	clk.add(endpointsMaxRefresh)
	c.invalidateDebounced()
	if _, ok := r3Hit(c, "k"); ok {
		t.Fatal("first request after the refresh interval was served the stale entry")
	}
	// That request is now refreshing and never finishes. Others are served
	// the stale entry, but only up to the maximum age.
	served := 0
	for i := 0; i < 400; i++ {
		clk.add(100 * time.Millisecond)
		c.invalidateDebounced()
		if _, ok := r3Hit(c, "k"); ok {
			served++
			if age := clk.now().Sub(computed); age > endpointsMaxAge {
				t.Fatalf("entry served at age %v, over the %v bound", age, endpointsMaxAge)
			}
		}
	}
	if served == 0 {
		t.Fatal("no request was served the stale entry while a refresh was in flight")
	}
}

// While one request re-aggregates, the others are served the stale entry and
// do not start the same aggregation.
func TestR3EndpointsCacheSingleRefreshInFlight(t *testing.T) {
	c, clk := r3CacheWithClock()
	r3Aggregate(t, c, clk, "k", "old.example.com", time.Second)
	clk.add(6 * time.Second)
	c.invalidateDebounced()
	if _, ok := r3Hit(c, "k"); ok {
		t.Fatal("stale entry served after its refresh interval")
	}
	token := c.generation() // the refresh starts
	for i := 0; i < 20; i++ {
		clk.add(50 * time.Millisecond)
		c.invalidateDebounced()
		if host, ok := r3Hit(c, "k"); !ok || host != "old.example.com" {
			t.Fatalf("request %d during the refresh got (%q, %v), want the stale entry", i, host, ok)
		}
	}
	if !c.setAt(token, "k", []store.Endpoint{{Host: "new.example.com"}}, "", 1, false) {
		t.Fatal("refresh result refused")
	}
	if host, _ := r3Hit(c, "k"); host != "new.example.com" {
		t.Fatalf("after the refresh the entry is %q", host)
	}
}

// A bulk change drops every entry at once, stale-serving or not, and refuses
// the result of an aggregation that started before it.
func TestR3EndpointsCacheBulkInvalidationIsImmediate(t *testing.T) {
	c, clk := r3CacheWithClock()
	r3Aggregate(t, c, clk, "k", "a.example.com", time.Second)
	c.invalidateDebounced()
	clk.add(100 * time.Millisecond)
	if _, ok := r3Hit(c, "k"); !ok {
		t.Fatal("stale entry not served inside its refresh interval")
	}
	inFlight := c.generation()
	before := c.bulkGeneration()
	c.invalidate()
	if c.bulkGeneration() == before {
		t.Fatal("invalidate did not advance the bulk generation")
	}
	if _, ok := r3Hit(c, "k"); ok {
		t.Fatal("entry survived a bulk invalidation")
	}
	if c.setAt(inFlight, "k", []store.Endpoint{{Host: "stale.example.com"}}, "", 1, false) {
		t.Fatal("a result computed before a bulk invalidation was stored")
	}
	if _, ok := r3Hit(c, "k"); ok {
		t.Fatal("pre-invalidation result is being served")
	}
}

// Failed aggregations never reach setAt; their tokens must not pile up.
func TestR3EndpointsCacheTokenTableIsBounded(t *testing.T) {
	c, _ := r3CacheWithClock()
	for i := 0; i < 10*endpointsMaxTokens; i++ {
		c.generation()
	}
	if n := len(c.tokens); n > endpointsMaxTokens {
		t.Fatalf("%d in-flight tokens retained, bound is %d", n, endpointsMaxTokens)
	}
	token := c.generation()
	if !c.setAt(token, "k", nil, "", 0, false) {
		t.Fatal("the newest token was evicted")
	}
}

// Traffic captured while an aggregation runs must not stop its result from
// being cached. Before, every capture scheduled a clear of the whole cache and
// a result computed across that clear was refused, so with traffic flowing and
// an aggregate slower than the two-second debounce nothing was ever cached and
// every Map poll paid the full aggregate.
func TestR3SlowAggregateIsCachedUnderLiveTraffic(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "a.example.com", Port: 443, Path: "/", HTTPVersion: "HTTP/1.1", Status: 200}); err != nil {
		t.Fatal(err)
	}
	live := &store.Flow{ID: 999, TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "live.example.com", Port: 443, Path: "/"}
	aggregations := 0
	h.epsCache.afterQuery = func() {
		aggregations++
		// A slow aggregate with captures arriving throughout.
		for i := 0; i < 23; i++ {
			h.FlowCaptured(live)
			time.Sleep(100 * time.Millisecond)
		}
	}
	get := func() {
		rec := httptest.NewRecorder()
		(&flowAPI{h}).listEndpoints(rec, httptest.NewRequest(http.MethodGet, "/api/endpoints", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("endpoints: %d %s", rec.Code, rec.Body.String())
		}
	}
	get()
	for i := 0; i < 5; i++ {
		h.FlowCaptured(live)
		get()
	}
	if aggregations != 1 {
		t.Fatalf("6 Map requests under live traffic ran the aggregate %d times, want 1", aggregations)
	}
}
