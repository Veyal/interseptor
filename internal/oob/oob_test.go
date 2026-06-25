package oob

import (
	"net/http/httptest"
	"testing"
)

func TestTokenFromPath(t *testing.T) {
	cases := map[string]string{
		"/oob/abc123":            "abc123",
		"/oob/abc123/x/y":        "abc123",
		"/oob/abc123?q=1":        "abc123",
		"/oob/":                  "",
		"/oob":                   "",
		"/nope/abc":              "",
	}
	for in, want := range cases {
		if got := TokenFromPath(in); got != want {
			t.Fatalf("TokenFromPath(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRecordAndList(t *testing.T) {
	c := New()
	var fired int
	c.SetNotifier(func() { fired++ })

	tok := c.Token()
	if len(tok) < 8 {
		t.Fatalf("token too short: %q", tok)
	}

	// A hit with a token is recorded; one without is ignored.
	c.Record(httptest.NewRequest("GET", "/oob/"+tok+"/ping?x=1", nil), "")
	c.Record(httptest.NewRequest("GET", "/favicon.ico", nil), "")

	if c.Count() != 1 {
		t.Fatalf("expected 1 interaction, got %d", c.Count())
	}
	if fired != 1 {
		t.Fatalf("notifier should fire once, fired %d", fired)
	}
	got := c.List()
	if len(got) != 1 || got[0].Token != tok || got[0].Query != "x=1" {
		t.Fatalf("unexpected interaction: %+v", got)
	}

	c.Clear()
	if c.Count() != 0 {
		t.Fatal("Clear should empty the catcher")
	}
}

func TestListNewestFirst(t *testing.T) {
	c := New()
	c.Record(httptest.NewRequest("GET", "/oob/a", nil), "")
	c.Record(httptest.NewRequest("GET", "/oob/b", nil), "")
	got := c.List()
	if len(got) != 2 || got[0].Token != "b" || got[1].Token != "a" {
		t.Fatalf("expected newest-first [b,a], got %+v", got)
	}
}
