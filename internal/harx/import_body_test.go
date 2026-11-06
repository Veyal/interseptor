package harx

import "testing"

// HAR 1.2 makes postData.text optional: an entry that lists the form fields only
// in postData.params must still import its request body, not a bodyless POST.
func TestParseBuildsFormBodyFromPostDataParams(t *testing.T) {
	doc := `{"log":{"version":"1.2","entries":[{` +
		`"startedDateTime":"2020-01-01T00:00:00Z","time":0,` +
		`"request":{"method":"POST","url":"https://example.com/login","httpVersion":"HTTP/1.1",` +
		`"headers":[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],` +
		`"postData":{"mimeType":"application/x-www-form-urlencoded","params":[` +
		`{"name":"user","value":"a b"},{"name":"pass","value":"p&ss"}]}},` +
		`"response":{"status":200,"httpVersion":"HTTP/1.1","headers":[],"content":{"size":0,"mimeType":"text/plain","text":""}}}]}}`
	entries, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got, want := string(entries[0].ReqBody), "user=a+b&pass=p%26ss"; got != want {
		t.Fatalf("request body = %q, want %q", got, want)
	}
}

// When the HAR carries both the wire text and the parsed params, the text is the
// authoritative body (it is what was actually sent).
func TestParsePrefersPostDataTextOverParams(t *testing.T) {
	doc := `{"log":{"version":"1.2","entries":[{` +
		`"startedDateTime":"2020-01-01T00:00:00Z","time":0,` +
		`"request":{"method":"POST","url":"https://example.com/x","httpVersion":"HTTP/1.1","headers":[],` +
		`"postData":{"mimeType":"application/json","text":"{\"a\":1}","params":[{"name":"a","value":"1"}]}},` +
		`"response":{"status":200,"httpVersion":"HTTP/1.1","headers":[],"content":{"size":0,"mimeType":"text/plain","text":""}}}]}}`
	entries, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := string(entries[0].ReqBody); got != `{"a":1}` {
		t.Fatalf("request body = %q, want the posted text", got)
	}
}
