package oob

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenFromPath(t *testing.T) {
	const tok = "0123456789abcdef"
	cases := map[string]string{
		"/oob/" + tok:           tok,
		"/oob/" + tok + "/x/y":  tok,
		"/oob/" + tok + "?q=1":  tok,
		"/oob/abc123":           "",
		"/oob/0123456789abcdeg": "",
		"/oob/":                 "",
		"/oob":                  "",
		"/nope/abc":             "",
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
	c.Record(httptest.NewRequest("GET", "/oob/000000000000000a", nil), "")
	c.Record(httptest.NewRequest("GET", "/oob/000000000000000b", nil), "")
	got := c.List()
	if len(got) != 2 || got[0].Token != "000000000000000b" || got[1].Token != "000000000000000a" {
		t.Fatalf("expected newest-first [b,a], got %+v", got)
	}
}

func TestRecordBoundsPublicCallbackMetadata(t *testing.T) {
	const tok = "0123456789abcdef"
	c := New()
	req := httptest.NewRequest("POST", "/oob/"+tok+"/"+strings.Repeat("p", maxInteractionPathBytes)+"?q="+strings.Repeat("q", maxInteractionQueryBytes+100), nil)
	req.Host = strings.Repeat("h", maxInteractionHostBytes+100)
	req.RemoteAddr = strings.Repeat("r", maxInteractionRemoteAddrBytes+100)
	req.Header.Set("User-Agent", strings.Repeat("u", maxInteractionUserAgentBytes+100))
	c.Record(req, strings.Repeat("b", maxInteractionBodyPreviewBytes+100))

	got := c.List()
	if len(got) != 1 {
		t.Fatalf("interactions = %d, want 1", len(got))
	}
	it := got[0]
	for name, tc := range map[string]struct {
		got string
		max int
	}{
		"path":        {it.Path, maxInteractionPathBytes},
		"query":       {it.Query, maxInteractionQueryBytes},
		"host":        {it.Host, maxInteractionHostBytes},
		"remoteAddr":  {it.RemoteAddr, maxInteractionRemoteAddrBytes},
		"userAgent":   {it.UserAgent, maxInteractionUserAgentBytes},
		"bodyPreview": {it.BodyPrev, maxInteractionBodyPreviewBytes},
	} {
		if len(tc.got) > tc.max {
			t.Errorf("%s retained %d bytes, max %d", name, len(tc.got), tc.max)
		}
	}
}
