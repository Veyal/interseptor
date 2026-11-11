package harx

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"testing"
)

// HAR 1.2 lets an emitter describe a multipart body by postData.params alone
// (no text). Re-serialising those as a=b&c=d under a multipart/form-data
// Content-Type produced a body no server can parse.
func TestParamsOnlyMultipartBodyIsMultipart(t *testing.T) {
	for name, doc := range map[string]string{
		"boundary in header": `{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-01-01T00:00:00Z","time":1,
 "request":{"method":"POST","url":"https://example.com/up","headers":[{"name":"Content-Type","value":"multipart/form-data; boundary=XX12"}],
  "postData":{"mimeType":"multipart/form-data; boundary=XX12","params":[{"name":"note","value":"a b&c"},{"name":"f","fileName":"a.png","contentType":"image/png"}]}},
 "response":{"status":200,"content":{}}}]}}`,
		"no boundary anywhere": `{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-01-01T00:00:00Z","time":1,
 "request":{"method":"POST","url":"https://example.com/up","headers":[{"name":"content-type","value":"multipart/form-data"}],
  "postData":{"mimeType":"multipart/form-data","params":[{"name":"note","value":"a b&c"},{"name":"f","fileName":"a.png","contentType":"image/png"}]}},
 "response":{"status":200,"content":{}}}]}}`,
	} {
		es, err := Parse([]byte(doc))
		if err != nil || len(es) != 1 {
			t.Fatalf("%s: %v %d", name, err, len(es))
		}
		e := es[0]
		ct := http.Header(e.ReqHeaders).Get("Content-Type")
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
			t.Fatalf("%s: content type %q", name, ct)
		}
		if n := len(http.Header(e.ReqHeaders).Values("Content-Type")); n != 1 {
			t.Fatalf("%s: %d Content-Type headers", name, n)
		}
		mr := multipart.NewReader(bytes.NewReader(e.ReqBody), params["boundary"])
		got := map[string]string{}
		files := map[string]string{}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s: body is not multipart: %v\n%s", name, err, e.ReqBody)
			}
			b, _ := io.ReadAll(p)
			got[p.FormName()] = string(b)
			if p.FileName() != "" {
				files[p.FormName()] = p.FileName() + "|" + p.Header.Get("Content-Type")
			}
		}
		if got["note"] != "a b&c" || files["f"] != "a.png|image/png" || len(got) != 2 {
			t.Fatalf("%s: parts %v files %v\n%s", name, got, files, e.ReqBody)
		}
	}

	// A urlencoded params-only entry keeps its form encoding.
	es, err := Parse([]byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-01-01T00:00:00Z","time":1,
 "request":{"method":"POST","url":"https://example.com/f","headers":[],"postData":{"mimeType":"application/x-www-form-urlencoded","params":[{"name":"a","value":"1 2"}]}},
 "response":{"status":200,"content":{}}}]}}`))
	if err != nil || string(es[0].ReqBody) != "a=1+2" {
		t.Fatalf("urlencoded params: %v %q", err, es[0].ReqBody)
	}
}
