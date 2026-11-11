package har

import (
	"strings"
	"testing"
)

// A HAR recorded in a browser holds WebSocket upgrades, data: and blob: URLs and
// extension pages next to the HTTP requests. None of them can be sent by the
// collection pipeline, so they must not become request items.
func TestNonHTTPEntriesAreSkippedAndReported(t *testing.T) {
	doc := `{"log":{"version":"1.2","entries":[
	 {"request":{"method":"GET","url":"https://api.example.com/v1/users","headers":[]}},
	 {"request":{"method":"GET","url":"wss://api.example.com/socket","headers":[]}},
	 {"request":{"method":"GET","url":"data:text/plain,hi","headers":[]}},
	 {"request":{"method":"GET","url":"blob:https://app.example.com/9f6c","headers":[]}},
	 {"request":{"method":"GET","url":"chrome-extension://abcdef/page.html","headers":[]}},
	 {"request":{"method":"POST","url":"HTTP://API.example.com/v1/login","headers":[]}}
	]}}`
	res, err := ParseHAR([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Stats.Requests != 2 {
		t.Fatalf("requests = %d, want 2 (only the http/https entries)", res.Report.Stats.Requests)
	}
	for _, it := range res.Items {
		if it.Kind == "folder" && (it.Name == "unknown-host" || strings.HasPrefix(it.Name, "data:")) {
			t.Errorf("folder %q was created for an entry that cannot be sent", it.Name)
		}
		if it.Kind == "request" && !strings.Contains(strings.ToLower(string(it.URL)), "http") {
			t.Errorf("non-HTTP request imported: %s", it.URL)
		}
		if strings.Contains(string(it.URL), "wss://") || strings.Contains(string(it.URL), "data:text") ||
			strings.Contains(string(it.URL), "blob:") || strings.Contains(string(it.URL), "chrome-extension") {
			t.Errorf("non-HTTP request imported: %s", it.URL)
		}
	}
	var got string
	for _, e := range res.Report.Entries {
		if e.Feature == "non-http-url" {
			got = e.Message
		}
	}
	if !strings.Contains(got, "4 ") {
		t.Fatalf("report does not say 4 entries were skipped: %q (entries %+v)", got, res.Report.Entries)
	}
	for _, s := range []string{"wss", "data", "blob", "chrome-extension"} {
		if !strings.Contains(got, s) {
			t.Errorf("report message %q does not name scheme %q", got, s)
		}
	}
	for _, e := range res.Report.Entries {
		if e.Feature == "target-host" && !strings.Contains(e.Message, "api.example.com") {
			t.Errorf("target-host entry for a host that was not imported: %q", e.Message)
		}
	}
}

// A capture holding nothing sendable is still an empty import, with the reason kept.
func TestOnlyNonHTTPEntriesIsEmptyWithReason(t *testing.T) {
	doc := `{"log":{"version":"1.2","entries":[{"request":{"method":"GET","url":"wss://api.example.com/socket","headers":[]}}]}}`
	res, err := ParseHAR([]byte(doc), Options{})
	if err != ErrEmpty {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
	found := false
	for _, e := range res.Report.Entries {
		found = found || e.Feature == "non-http-url"
	}
	if !found {
		t.Fatalf("no non-http-url report entry: %+v", res.Report.Entries)
	}
}

func TestNonHTTPSchemeKeepsHostPortURLs(t *testing.T) {
	for raw, want := range map[string]string{
		"https://example.com/a":       "",
		"HTTP://example.com/a":        "",
		"example.com:8080/a":          "",
		"localhost:3000":              "",
		"/relative/path?x=a:b":        "",
		"wss://example.com/s":         "wss",
		"WS://example.com/s":          "ws",
		"data:text/plain,hi":          "data",
		"blob:https://example.com/1":  "blob",
		"about:blank":                 "about",
		"chrome-extension://abc/x.js": "chrome-extension",
		"file:///etc/hosts":           "file",
	} {
		if got := nonHTTPScheme(raw); got != want {
			t.Errorf("nonHTTPScheme(%q) = %q, want %q", raw, got, want)
		}
	}
}
