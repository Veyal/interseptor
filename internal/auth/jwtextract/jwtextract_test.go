package jwtextract

import "testing"

func TestExtractBearer(t *testing.T) {
	tok, src := Extract(Input{
		ReqHeaders: map[string][]string{
			"Authorization": {"Bearer eyJhbG.a.b"},
		},
	})
	if tok != "eyJhbG.a.b" || src != "bearer" {
		t.Fatalf("got %q %q", tok, src)
	}
}

func TestExtractJSONLoginToken(t *testing.T) {
	body := []byte(`{"login_token":"eyJhbG.a.b","success":true}`)
	tok, src := Extract(Input{ResBody: body})
	if tok != "eyJhbG.a.b" || src != "json:login_token" {
		t.Fatalf("got %q %q", tok, src)
	}
}

func TestExtractPathSegment(t *testing.T) {
	tok, src := Extract(Input{Path: "/authBroker/eyJhbG.a.b/callback"})
	if tok != "eyJhbG.a.b" || src != "path" {
		t.Fatalf("got %q %q", tok, src)
	}
}

func TestExtractLoginURL(t *testing.T) {
	body := []byte(`{"login_url":"https://sso.example.com/authBroker/eyJhbG.a.b?x=1"}`)
	tok, src := Extract(Input{ResBody: body})
	if tok != "eyJhbG.a.b" || src != "json:login_url:path" {
		t.Fatalf("got %q %q", tok, src)
	}
}

func TestExtractQuery(t *testing.T) {
	tok, src := Extract(Input{RawQuery: "token=eyJhbG.a.b&x=1"})
	if tok != "eyJhbG.a.b" || src != "query:token" {
		t.Fatalf("got %q %q", tok, src)
	}
}

func TestReplayPath(t *testing.T) {
	if p := ReplayPath("/authBroker/old.jwt.here", "old.jwt.here"); p != "/authBroker/old.jwt.here" {
		t.Fatalf("embedded: %q", p)
	}
	if p := ReplayPath("/api/login", "tok.a.b"); p != "/authBroker/tok.a.b" {
		t.Fatalf("default: %q", p)
	}
}

// A JWT's header segment is base64url of a JSON object, so it decodes to text
// starting with "{". The old three-dot shape test accepted any "a.b.c", which
// made version strings and asset filenames look like tokens: a flow for
// /api/v1.2.3/users or /static/app.min.js then drove path-mode replay with a
// bogus "JWT".
func TestExtractIgnoresNonJWTThreePartValues(t *testing.T) {
	for _, path := range []string{
		"/static/app.min.js",
		"/api/v1.2.3/users",
		"/assets/vendor.1.2.3.min.js",
	} {
		if tok, src := Extract(Input{Path: path}); tok != "" {
			t.Errorf("Extract(path=%q) = %q (%s), want no token", path, tok, src)
		}
	}
	// A real JWT in the path is still found.
	if tok, src := Extract(Input{Path: "/authBroker/eyJhbG.a.b/cb"}); tok != "eyJhbG.a.b" || src != "path" {
		t.Errorf("real JWT in path: got %q %q", tok, src)
	}
	// A login URL for a versioned app must not yield the version as a token.
	body := []byte(`{"login_url":"https://sso.example.com/app/v1.2.3/callback"}`)
	if tok, src := Extract(Input{ResBody: body}); tok != "" {
		t.Errorf("versioned login_url = %q (%s), want no token", tok, src)
	}
	// A JWT embedded in a login URL is still found.
	body = []byte(`{"login_url":"https://sso.example.com/authBroker/eyJhbG.a.b?x=1"}`)
	if tok, src := Extract(Input{ResBody: body}); tok != "eyJhbG.a.b" || src != "json:login_url:path" {
		t.Errorf("embedded JWT in login_url: got %q %q", tok, src)
	}
}
