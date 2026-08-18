package burpx

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseSavedItemsXML(t *testing.T) {
	crlf := string([]byte{13, 10})
	req := append([]byte(strings.Join([]string{
		"POST /submit?q=1 HTTP/1.1", "Host: example.com", "Content-Type: application/octet-stream", "X-Test: one", "", "",
	}, crlf)), 0, 0xff)
	res := append([]byte(strings.Join([]string{
		"HTTP/1.1 201 Created", "Content-Type: application/octet-stream", "X-Result: ok", "", "",
	}, crlf)), 1, 0xfe)
	xmlDoc := `<?xml version="1.0"?>
<!DOCTYPE items [<!ELEMENT items (item*)>]>
<items burpVersion="2026.7" exportTime="Tue Aug 18 10:00:00 UTC 2026">
  <item>
    <time>Tue Aug 18 09:30:00 UTC 2026</time>
    <url><![CDATA[https://example.com/submit?q=1]]></url>
    <host ip="203.0.113.10">example.com</host><port>443</port><protocol>https</protocol>
    <method>POST</method><path><![CDATA[/submit?q=1]]></path>
    <request base64="true"><![CDATA[` + base64.StdEncoding.EncodeToString(req) + `]]></request>
    <status>201</status><responselength>85</responselength><mimetype>binary</mimetype>
    <response base64="true"><![CDATA[` + base64.StdEncoding.EncodeToString(res) + `]]></response>
    <comment><![CDATA[Imported evidence note]]></comment>
  </item>
</items>`

	var got []Entry
	n, err := Parse(strings.NewReader(xmlDoc), func(e Entry) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if n != 1 || len(got) != 1 {
		t.Fatalf("count = %d, entries = %d", n, len(got))
	}
	e := got[0]
	if e.Method != "POST" || e.URL != "https://example.com/submit?q=1" || e.HTTPVersion != "HTTP/1.1" || e.Status != 201 {
		t.Fatalf("metadata = %+v", e)
	}
	if e.ReqHeaders.Get("X-Test") != "one" || e.ResHeaders.Get("X-Result") != "ok" {
		t.Fatalf("headers = req %#v res %#v", e.ReqHeaders, e.ResHeaders)
	}
	if !bytes.Equal(e.ReqBody, []byte{0, 0xff}) || !bytes.Equal(e.ResBody, []byte{1, 0xfe}) {
		t.Fatalf("bodies = req %x res %x", e.ReqBody, e.ResBody)
	}
	if e.Mime != "application/octet-stream" || e.Comment != "Imported evidence note" || e.TS.IsZero() {
		t.Fatalf("metadata tail = %+v", e)
	}
}

func TestParsePlainSavedItemAndReconstructURL(t *testing.T) {
	xmlDoc := `<items><item><host>example.com</host><port>8080</port><protocol>http</protocol>` +
		`<request base64="false">GET /plain HTTP/1.0&#10;Host: example.com:8080&#10;&#10;</request>` +
		`<response base64="false">HTTP/1.0 204 No Content&#10;&#10;</response></item></items>`
	var got Entry
	_, err := Parse(strings.NewReader(xmlDoc), func(e Entry) error { got = e; return nil })
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.URL != "http://example.com:8080/plain" || got.Method != "GET" || got.Status != 204 || got.HTTPVersion != "HTTP/1.0" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestParseRejectsMalformedAndNonSavedItemsFormats(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{name: "native project", doc: "Burp project file binary data", want: "native .burp"},
		{name: "wrong XML root", doc: `<issues burpVersion="x"></issues>`, want: "saved-items XML"},
		{name: "bad base64", doc: `<items><item><url>https://example.com/</url><request base64="true">!!!</request></item></items>`, want: "base64"},
		{name: "entity", doc: `<!DOCTYPE items [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><items><item><url>&xxe;</url></item></items>`, want: "entity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.doc), func(Entry) error { return nil })
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
