package httplines

import "testing"

func TestParseRawRequest(t *testing.T) {
	method, path, hdrs, body, err := ParseRawRequest("GET /api/x HTTP/1.1\nHost: app.test\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if method != "GET" || path != "/api/x" {
		t.Fatalf("method/path = %s %s", method, path)
	}
	if hdrs["Host"] == nil || hdrs["Host"][0] != "app.test" {
		t.Fatalf("host = %v", hdrs["Host"])
	}
	if len(body) != 0 {
		t.Fatalf("body = %q", body)
	}

	_, _, _, body, err = ParseRawRequest("POST / HTTP/1.1\nHost: x\n\nhello")
	if err != nil || string(body) != "hello" {
		t.Fatalf("body parse: err=%v body=%q", err, body)
	}
}
