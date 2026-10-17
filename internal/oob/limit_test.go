package oob

import (
	"net/http/httptest"
	"testing"
)

// The ring keeps the newest MaxInteractions hits and counts what it evicted,
// so the UI can say how many older callbacks are gone; Clear resets both.
func TestRingCountsDroppedInteractions(t *testing.T) {
	c := New()
	tok := c.Token()
	for i := 0; i < MaxInteractions+3; i++ {
		c.Record(httptest.NewRequest("GET", "http://example.com/oob/"+tok, nil), "")
	}
	if c.Count() != MaxInteractions {
		t.Fatalf("count = %d, want %d", c.Count(), MaxInteractions)
	}
	if c.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", c.Dropped())
	}
	c.Clear()
	if c.Count() != 0 || c.Dropped() != 0 {
		t.Fatalf("after clear count=%d dropped=%d", c.Count(), c.Dropped())
	}
}
