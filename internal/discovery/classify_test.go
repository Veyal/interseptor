package discovery

import "testing"

func TestIsAPIPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// API segment signals.
		{"/api/users", true},
		{"/api", true},
		{"/apis/v3", true},
		{"/app/api/orders", true},
		{"/graphql", true},
		{"/graphiql", true},
		{"/v1/users", true},
		{"/v2", true},
		{"/v10/data", true},
		{"/api/v1beta1/things", true},
		{"/rest/products", true},
		{"/jsonrpc", true},
		{"/oauth2/token", true},
		{"/wp-json/wp/v2/posts", true},
		// Data extensions.
		{"/data/export.json", true},
		{"/feed.xml", true},
		{"/schema.graphql", true},
		// Static assets must never be tagged.
		{"/styles/main.css", false},
		{"/bundle.js", false},
		{"/app.min.js", false},
		{"/logo.png", false},
		{"/icon.svg", false},
		{"/fonts/roboto.woff2", false},
		{"/favicon.ico", false},
		{"/index.html", false},
		{"/readme.txt", false},
		// Plain pages / non-API words that look API-ish.
		{"/", false},
		{"", false},
		{"/video/list", false}, // "video" must not match the version rule
		{"/view", false},
		{"/about", false},
		{"/users", false},
		{"/version", false},
		// A static extension wins even if a segment looks API-ish.
		{"/api/widget.css", false},
		{"/v1/logo.png", false},
		// Query/fragment are ignored.
		{"/api/users?id=1", true},
		{"/admin#api", false},
	}
	for _, c := range cases {
		if got := IsAPIPath(c.path); got != c.want {
			t.Errorf("IsAPIPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestAutoTagWiresThroughResult is an integration-style check that a discovered
// API path carries enough signal (its Path) for the recorder to tag it. It
// mirrors TestRecorderSetsFlowID but asserts on IsAPIPath of the recorded path.
func TestDiscoveredAPIPathClassified(t *testing.T) {
	fp := newFakeProbe(map[string]Outcome{
		"https://t/api/v1/users": {Status: 200, Length: 100},
		"https://t/styles.css":   {Status: 200, Length: 100},
	})
	e := New()
	e.SetProbe(fp.probe())
	var tagged []string
	e.SetRecorder(func(r Result) int64 {
		if IsAPIPath(r.Path) {
			tagged = append(tagged, r.Path)
		}
		return 1
	})
	runSync(t, e, Spec{
		BaseURL: "https://t/", Words: []string{"api/v1/users", "styles.css"},
		Threads: 2, AutoTagAPI: true,
	})
	if len(tagged) != 1 || tagged[0] != "/api/v1/users" {
		t.Fatalf("expected only /api/v1/users classified as API, got %v", tagged)
	}
}
