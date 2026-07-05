package oob

import (
	"net/http/httptest"
	"sync"
	"testing"
)

func TestMintCorrelatedRoundTrip(t *testing.T) {
	c := New()
	tok, url := c.MintCorrelated(7, "cand-1", 42)
	if len(tok) < 8 {
		t.Fatalf("token too short: %q", tok)
	}
	if url != "" {
		t.Fatalf("MintCorrelated should return empty url with no base, got %q", url)
	}

	corr, ok := c.CorrelationFor(tok)
	if !ok {
		t.Fatalf("CorrelationFor(%q) not found", tok)
	}
	if corr.Token != tok || corr.RunID != 7 || corr.CandidateID != "cand-1" || corr.ProbeFlowID != 42 {
		t.Fatalf("unexpected correlation: %+v", corr)
	}
	if corr.InjectedAt == 0 {
		t.Fatal("InjectedAt should be set")
	}
}

func TestMintCorrelatedURL(t *testing.T) {
	c := New()
	tok, url := c.MintCorrelatedURL(1, "c", 2, "http://oob.example.com/oob/")
	if want := "http://oob.example.com/oob/" + tok; url != want {
		t.Fatalf("url=%q want %q", url, want)
	}
}

func TestInteractionsForToken(t *testing.T) {
	c := New()
	tok, _ := c.MintCorrelated(1, "c", 2)

	// No callback yet.
	if got := c.InteractionsForToken(tok); len(got) != 0 {
		t.Fatalf("expected no interactions before callback, got %d", len(got))
	}

	// A callback arrives for the minted token.
	c.Record(httptest.NewRequest("GET", "/oob/"+tok+"/ping?x=1", nil), "body")
	got := c.InteractionsForToken(tok)
	if len(got) != 1 || got[0].Token != tok || got[0].BodyPrev != "body" {
		t.Fatalf("expected 1 interaction for token, got %+v", got)
	}

	// Two callbacks → both returned, newest first.
	c.Record(httptest.NewRequest("GET", "/oob/"+tok+"/again", nil), "second")
	got = c.InteractionsForToken(tok)
	if len(got) != 2 || got[0].BodyPrev != "second" || got[1].BodyPrev != "body" {
		t.Fatalf("expected 2 interactions newest-first, got %+v", got)
	}
}

func TestUnknownToken(t *testing.T) {
	c := New()
	if _, ok := c.CorrelationFor("deadbeef"); ok {
		t.Fatal("CorrelationFor unknown token should be false")
	}
	if got := c.InteractionsForToken("deadbeef"); len(got) != 0 {
		t.Fatalf("InteractionsForToken unknown should be empty, got %d", len(got))
	}
	if got := c.InteractionsForToken(""); got != nil {
		t.Fatalf("InteractionsForToken empty should be nil, got %+v", got)
	}
}

// Correlation must not perturb the existing manual-OOB interaction ring.
func TestCorrelationDoesNotAffectRing(t *testing.T) {
	c := New()
	c.MintCorrelated(1, "c", 2) // pure metadata; must not touch the ring
	if c.Count() != 0 {
		t.Fatalf("minting a correlation must not add interactions, got %d", c.Count())
	}
	tok := c.Token()
	c.Record(httptest.NewRequest("GET", "/oob/"+tok, nil), "")
	if c.Count() != 1 {
		t.Fatalf("manual OOB Record still works, got %d", c.Count())
	}
}

func TestCorrelationBounded(t *testing.T) {
	c := New()
	first, _ := c.MintCorrelated(0, "first", 0)
	for i := 0; i < maxCorrelations+10; i++ {
		c.MintCorrelated(int64(i), "x", int64(i))
	}
	if _, ok := c.CorrelationFor(first); ok {
		t.Fatal("oldest correlation should have been evicted past the cap")
	}
	c.cmu.Lock()
	n := len(c.corr)
	c.cmu.Unlock()
	if n > maxCorrelations {
		t.Fatalf("correlation map unbounded: %d > %d", n, maxCorrelations)
	}
}

func TestCorrelationConcurrent(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(n int) {
			defer wg.Done()
			tok, _ := c.MintCorrelated(int64(n), "c", int64(n))
			c.CorrelationFor(tok)
		}(i)
		go func(n int) {
			defer wg.Done()
			c.Record(httptest.NewRequest("GET", "/oob/tok/x", nil), "")
		}(i)
		go func() { defer wg.Done(); c.InteractionsForToken("tok") }()
	}
	wg.Wait()
}
